package agentworker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// hybridTrees implements workflow.Trees by splitting operations: Prepare
// and Push (network ops needing the daemon's forge credential) proxy over
// worktreerpc; CommitAll/Diff/ChangedFiles/ChangedLines are local git
// operations run directly against the container's bind-mounted worktree.
//
// Prepare returns the bind-mounted worktree that archied prepared before the
// container started. The sandbox has no remote prepare capability.
type remoteTrees interface {
	Push(ctx context.Context) error
}

type hybridTrees struct {
	push     remoteTrees
	local    *worktree.Manager
	localDir string
	branch   string
	// worktreeUID and worktreeGID are the daemon's own host UID/GID
	// (WORKTREE_UID/WORKTREE_GID, set by containerEnv), or -1 when unset.
	// The agent runs as root inside the container, so a commit it writes
	// to the bind-mounted worktree lands owned by UID 0 on the host; Push
	// reconciles ownership back to the daemon's UID before handing off,
	// since the daemon -- running as its own non-root host user -- is the
	// one that reads those objects to actually push (archie-core#520).
	worktreeUID, worktreeGID int
}

func (h *hybridTrees) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error) {
	return h.localDir, h.branch, nil
}

func (h *hybridTrees) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	changed, commitErr := h.local.CommitAll(ctx, dir, message)
	if ownershipErr := h.reconcileOwnership(dir); ownershipErr != nil {
		ownershipErr = fmt.Errorf("reconcile worktree ownership after commit: %w", ownershipErr)
		return changed, errors.Join(commitErr, ownershipErr)
	}
	return changed, commitErr
}

func (h *hybridTrees) Push(ctx context.Context, dir, branch string) error {
	if err := h.reconcileOwnership(dir); err != nil {
		return fmt.Errorf("reconcile worktree ownership before push: %w", err)
	}
	return h.push.Push(ctx)
}

func (h *hybridTrees) reconcileOwnership(dir string) error {
	if h.worktreeUID < 0 || h.worktreeGID < 0 {
		return nil
	}
	return chownTree(dir, h.worktreeUID, h.worktreeGID)
}

// restoreWorktreeOwnership returns the deferred half of the ownership
// contract: the agent runs as root inside the container, so a run that ends
// without ever reaching CommitAll or Push (a gate failure that parks the task,
// a stage error, an early return) has to hand the bind-mounted worktree back to
// the daemon's own UID on its way out. The daemon is the process that later
// cleans and resets that directory as a non-root host user, and it cannot even
// unlink inside a root-owned directory -- so a parked attempt otherwise left a
// worktree the next attempt could not start from, no matter what the refresh
// did.
//
// Returns the func rather than deferring internally so the caller's own defer
// runs it at the caller's return, not at this function's.
func restoreWorktreeOwnership(trees *hybridTrees, dir string, log *slog.Logger) func() {
	return func() {
		if err := trees.reconcileOwnership(dir); err != nil {
			log.Warn("worktree ownership restore failed", "dir", dir, "err", err)
		}
	}
}

func (h *hybridTrees) Diff(ctx context.Context, dir, base string) (string, error) {
	return h.local.Diff(ctx, dir, base)
}

func (h *hybridTrees) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	return h.local.ChangedFiles(ctx, dir, base)
}

func (h *hybridTrees) Snapshot(ctx context.Context, dir, destDir string) error {
	return h.local.Snapshot(ctx, dir, destDir)
}

// Resume is a no-op here for the same reason Prepare is: resuming a PR
// branch needs the daemon's forge credential to fetch, so archied must run
// it against the bind-mounted worktree before the container starts, the
// same way it runs Prepare today. The container's worktree is already
// resumed by the time a remediate run reaches this stage.
func (h *hybridTrees) Resume(ctx context.Context, dir, branch string) error {
	return nil
}

// Dir returns the bind-mounted worktree path archied already prepared,
// ignoring its arguments -- the sandbox has no way to independently derive
// or verify the path, same as Prepare above.
func (h *hybridTrees) Dir(owner, repo string, issue int) string {
	return h.localDir
}

func (h *hybridTrees) ChangedLines(ctx context.Context, dir, base string) (int, error) {
	return h.local.ChangedLines(ctx, dir, base)
}

// ChangedFileStats is the capability the workflow captures a change through.
// It is not part of workflow.Trees: it is asserted for as the unexported
// optional interface package workflow declares, so a Trees implementation
// without it degrades to no capture. Without this forwarder the in-container
// path -- which is every production run, archie-agent executing the whole
// workflow -- would be exactly that silent no-op.
func (h *hybridTrees) ChangedFileStats(ctx context.Context, dir, base string) (task.ChangeStats, error) {
	return h.local.ChangedFileStats(ctx, dir, base)
}

// HasUncommittedChanges is the capability the diff-rules placement guard reads
// to tell "nothing has changed" from "not committed yet". It is not part of
// workflow.Trees for the same reason ChangedFileStats is not, and without this
// forwarder the in-container path -- every production run -- would answer the
// guard for the wrong reason.
func (h *hybridTrees) HasUncommittedChanges(ctx context.Context, dir string) (bool, error) {
	return h.local.HasUncommittedChanges(ctx, dir)
}

var _ workflow.Trees = (*hybridTrees)(nil)

// chownTree recursively chowns dir to uid:gid so the daemon -- running as
// its own host user -- can read loose objects the agent committed as root
// inside the container.
//
// Lchown, not Chown: a repo can legitimately track a symlink whose target
// doesn't exist inside the container (a relative link outside the
// checkout, or to a tool the image doesn't ship). Chown follows the link
// and fails on a dangling target, which would abort a push that would
// otherwise have succeeded -- worse than the permission error this is
// fixing. Lchown changes the link itself and never touches the target.
func chownTree(dir string, uid, gid int) error {
	return filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}

// worktreeOwnerID parses a WORKTREE_UID/WORKTREE_GID environment value,
// returning -1 (unset/unconfigured) when it is empty or not a valid
// non-negative integer.
func worktreeOwnerID(env string) int {
	if env == "" {
		return -1
	}
	id, err := strconv.Atoi(env)
	if err != nil || id < 0 {
		return -1
	}
	return id
}

type taskDependencies struct {
	forge  workflow.Forger
	store  workflow.Store
	trees  remoteTrees
	events agentexec.EventPublisher
	// steps is the workflow step vocabulary pinned definitions are compiled
	// against, registered at the composition root and injected: this is the
	// executing half of the contract whose validating half is the control
	// plane's (archie-core-fwmp).
	steps *workflow.Manager
}

type runnerFactory func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner

func newTaskRunner(providers map[string]agentexec.Provider, log *slog.Logger) agentexec.Runner {
	return agentexec.NewLoopRunner(agentexec.NewRuntime(providers), log)
}

// applyToolLimits wires the task's carried tool policy and its agent
// profile's tool allowlist into a *LoopRunner: the result cap/spill, so a
// worker-executed stage enforces the same limits the daemon's own chat path
// applies (config.Config.Tools.Policy, carried non-secret via
// TaskConfig.ToolPolicy), and the tools the profile allows. A runner that
// isn't a *LoopRunner (e.g. a test fake) is left untouched.
func applyToolLimits(agent agentexec.Runner, policy config.ToolPolicy, allow []string) {
	runner, ok := agent.(*agentexec.LoopRunner)
	if !ok {
		return
	}
	runner.Limits = agentexec.ToolLimits{
		MaxResultChars: policy.MaxResultChars,
		SpillDir:       policy.SpillDir,
	}
	runner.AllowTools = allow
}

// stageRunners builds the runner every agent stage uses, and the reviewer.
func stageRunners(req taskrun.Request, mcpSet *mcpProviderSet, newRunner runnerFactory, log *slog.Logger) (agentexec.Runner, workflow.Reviewer, error) {
	if req.Harness != nil {
		// The built-in loop has no route to a model from a Kit container, so
		// the review stage parks rather than running on it.
		return agentexec.HarnessStages{Runner: agentexec.NewHarnessRunner(nil), Spec: *req.Harness}, nil, nil
	}
	var agent agentexec.Runner
	if mcpSet != nil && mcpSet.registry != nil {
		agent = agentexec.NewLoopRunner(agentexec.NewRuntime(req.Providers), log, mcpSet.registry)
	} else {
		agent = newRunner(req.Providers, log)
	}
	if agent == nil {
		return nil, nil, fmt.Errorf("no agent runner configured for task %d", req.Task.ID)
	}
	applyToolLimits(agent, req.Cfg.ToolPolicy, req.Tools)
	return agent, newReviewerFor(req), nil
}

// routeTask remains available for routing-only callers. Production execution
// compiles the request's pinned YAML directly.
func routeTask(req taskrun.Request, registry workflow.Registry) workflow.Workflow {
	workflow.SetKindWorkflows(req.KindWorkflows)
	workflow.SetLabelWorkflows(req.LabelWorkflows)
	return workflow.Route(req.Task, registry)
}

// runTask compiles the immutable database definition carried by the request,
// runs it, and reports its terminal outcome.
// Store remains archied's authority; Response.Task is a logging snapshot.
func runTask(ctx context.Context, req taskrun.Request, dependencies taskDependencies, newRunner runnerFactory, workDir string, log *slog.Logger) (*taskrun.Response, error) {
	wf, err := CompilePinnedWorkflow(&req, dependencies.steps)
	if err != nil {
		return nil, err
	}

	trees := &hybridTrees{
		push: dependencies.trees,
		local: &worktree.Manager{
			WorkDir:  workDir,
			BotUser:  req.Cfg.BotUser,
			BotEmail: req.Cfg.BotEmail,
		},
		localDir:    workDir,
		branch:      req.Task.Branch,
		worktreeUID: worktreeOwnerID(os.Getenv("WORKTREE_UID")),
		worktreeGID: worktreeOwnerID(os.Getenv("WORKTREE_GID")),
	}
	// The agent process runs as root inside the container, so everything this
	// run writes into the bind-mounted worktree is owned by UID 0 on the host;
	// CommitAll and Push hand it back on the success path (archie-core#520).
	// Doing the same here covers every OTHER way a run can end -- a gate
	// failure that parks the task, a stage error, an early return -- because
	// the daemon is the one that later cleans and resets this directory, as its
	// own non-root host user, and it cannot even unlink a root-owned directory.
	// Without this, a parked attempt left a worktree the next attempt could not
	// start from.
	defer restoreWorktreeOwnership(trees, workDir, log)()

	// Start MCP providers and build a local tool registry.
	mcpSet, mcpErr := startMCPProviders(ctx, req.MCPServers, log)
	if mcpSet != nil {
		defer mcpSet.cleanup(ctx, log)
	}
	if mcpErr != nil {
		log.Warn("mcp providers had errors, continuing with available tools", "err", mcpErr)
	}

	agent, reviewer, err := stageRunners(req, mcpSet, newRunner, log)
	if err != nil {
		return nil, err
	}
	agent = persistentRunner{Runner: agent, enabled: req.Repo.PersistentStorage}

	// A workflow run in this process publishes to an in-process *events.Bus
	// the daemon cannot see -- archied and archie-agent are separate
	// processes connected only by NATS. Without this bridge, tc.Emit is a
	// silent no-op for every stage/outcome/park event this run produces,
	// which is why the dashboard timeline showed nothing for any task
	// executed through the container/NATS path (archie-core-518). Nil
	// dependencies.events (a caller with no NATS connection, e.g. tests
	// that construct taskDependencies directly) leaves bus nil, and
	// TaskContext.Emit is already nil-safe.
	var bus *events.Bus
	if dependencies.events != nil {
		bus = events.NewBus()
		sub := bus.Subscribe(64)
		defer sub.Close()
		go agentexec.ForwardTaskEvents(sub, dependencies.events, req.Task.ID, log)
	}

	tc := &workflow.TaskContext{
		Task:     req.Task,
		Repo:     req.Repo,
		Cfg:      req.Cfg.ToConfig(),
		Forge:    dependencies.forge,
		Store:    dependencies.store,
		Trees:    trees,
		Agent:    agent,
		Reviewer: reviewer,
		Bus:      bus,
		Log:      log,
	}
	if req.Repo.PersistentStorage {
		memory, readErr := readProjectMemory(storage.MemoryPath)
		if readErr != nil {
			return nil, fmt.Errorf("read project memory: %w", readErr)
		}
		if memory != "" {
			tc.SystemPrompt = func() string { return memory }
		}
	}

	workflow.Run(ctx, wf, tc)

	return &taskrun.Response{
		Task:             tc.Task,
		Status:           tc.Outcome.Status,
		AgentVersion:     Version(),
		AgentInstallType: installtype.Type(),
	}, nil
}

// CompilePinnedWorkflow resolves and compiles the immutable definition the task
// request carries, against this process's step vocabulary. Production execution
// and the workflow step-vocabulary contract test
// (internal/app/controlplane/step_vocabulary_agreement_test.go) both enter
// through it.
//
// A request that carries no definition is routed and pinned from the shipped
// definitions, which writes the resolved workflow name, YAML, and digest back
// onto req and req.Task: the run's TaskContext carries that task record, so the
// pin has to land on the request the caller handed in.
func CompilePinnedWorkflow(req *taskrun.Request, steps *workflow.Manager) (workflow.Workflow, error) {
	// steps is this process's required step vocabulary, so a nil one is a wiring
	// mistake at the composition root (productionWorkerDependencies) rather than
	// a runtime condition: name it instead of dereferencing it.
	if steps == nil {
		return workflow.Workflow{}, errors.New("compile pinned workflow: no step vocabulary: the composition root must register the provider set (infrastructure/workflowsteps.NewManager) before the first compile")
	}
	// One resolution for the whole function: both compiles below read the
	// vocabulary the composition root registered, never the builtin registry.
	vocabulary := steps.Registry()
	if req.WorkflowDefinition == "" {
		shipped := workflow.ShippedDefinitions()
		routing := make(workflow.Registry, len(shipped.Definitions))
		for _, definition := range shipped.Definitions {
			compiled, compileErr := workflow.ParseAndCompile(definition.YAML, vocabulary)
			if compileErr != nil {
				return workflow.Workflow{}, fmt.Errorf("compile shipped workflow definition: %w", compileErr)
			}
			routing[definition.ID] = compiled
		}
		selected := routeTask(*req, routing)
		req.Task.Workflow = selected.Name
		entry, ok := shipped.DefinitionByID(selected.Name)
		if !ok {
			return workflow.Workflow{}, fmt.Errorf("task has no pinned workflow definition")
		}
		req.WorkflowDefinition = entry.YAML
		req.Task.WorkflowDefinitionYAML = entry.YAML
		req.Task.WorkflowDefinitionDigest = workflow.DigestDefinition(entry.YAML)
	}
	wf, err := workflow.ParseAndCompile(req.WorkflowDefinition, vocabulary)
	if err != nil {
		return workflow.Workflow{}, fmt.Errorf("compile pinned workflow definition: %w", err)
	}
	if wf.Name != req.Task.Workflow || workflow.DigestDefinition(req.WorkflowDefinition) != req.Task.WorkflowDefinitionDigest {
		return workflow.Workflow{}, fmt.Errorf("pinned workflow definition does not match task identity")
	}
	return wf, nil
}
