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

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/prsource"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// hybridTrees implements workflow.Trees: Push goes to the daemon over
// worktreerpc; local git operations run on the bind-mounted worktree.
type remoteTrees interface {
	Push(ctx context.Context) error
}

type hybridTrees struct {
	push     remoteTrees
	local    *worktree.Manager
	localDir string
	branch   string
	// worktreeUID and worktreeGID are the daemon's host UID/GID, or -1. Push
	// chowns the worktree back to them.
	worktreeUID, worktreeGID int
}

// Prepare returns the worktree the daemon already prepared and its branch.
func (h *hybridTrees) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string, target workflow.PrepareTarget) (dir, branch string, err error) {
	if target != workflow.PrepareFresh {
		return h.localDir, string(target), nil
	}
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

// restoreWorktreeOwnership returns a func that chowns the worktree back to
// the daemon's UID, for every way a run can end.
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

func (h *hybridTrees) ChangedLines(ctx context.Context, dir, base string) (int, error) {
	return h.local.ChangedLines(ctx, dir, base)
}

// ChangedFileStats forwards to the local worktree manager.
func (h *hybridTrees) ChangedFileStats(ctx context.Context, dir, base string) (task.ChangeStats, error) {
	return h.local.ChangedFileStats(ctx, dir, base)
}

// HasUncommittedChanges forwards to the local worktree manager.
func (h *hybridTrees) HasUncommittedChanges(ctx context.Context, dir string) (bool, error) {
	return h.local.HasUncommittedChanges(ctx, dir)
}

var _ workflow.Trees = (*hybridTrees)(nil)

// chownTree recursively lchowns dir to uid:gid.
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
	calls  task.Caller
	trees  remoteTrees
	events agentexec.EventPublisher
	// steps is the workflow step vocabulary pinned definitions are compiled
	// against, registered at the composition root and injected: this is the
	// executing half of the contract whose validating half is the control
	// plane's.
	steps *workflow.Manager
	// guardrails is the guardrail engine agent stages record tool outcomes
	// against. It is built by this process's composition root (the workflow
	// runs in archie-agent, so the daemon's engine cannot reach it) and set on
	// every TaskContext; nil means guardrails are disabled.
	guardrails *tools.GuardrailEngine
}

type runnerFactory func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner

func newTaskRunner(providers map[string]agentexec.Provider, log *slog.Logger) agentexec.Runner {
	return agentexec.NewLoopRunner(agentexec.NewRuntime(providers), log)
}

// applyToolLimits sets the task's tool policy and allowlist on a
// *LoopRunner. Other runners are untouched.
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

// stageRunners builds the runner every agent stage uses. llm is the task's
// shared model runtime (the one its MCP providers answer sampling from); the
// registry path reuses it rather than building a second one.
func stageRunners(req taskrun.Request, mcpSet *mcpProviderSet, newRunner runnerFactory, llm *runtime.Runtime, log *slog.Logger) (agentexec.Runner, error) {
	if req.Harness != nil {
		// The built-in loop has no route to a model from a Kit container.
		return agentexec.HarnessStages{Runner: agentexec.NewHarnessRunner(nil), Spec: *req.Harness}, nil
	}
	var agent agentexec.Runner
	if mcpSet != nil && mcpSet.registry != nil {
		agent = agentexec.NewLoopRunner(llm, log, mcpSet.registry)
	} else {
		agent = newRunner(req.Providers, log)
	}
	if agent == nil {
		return nil, fmt.Errorf("no agent runner configured for task %d", req.Task.ID)
	}
	applyToolLimits(agent, req.Cfg.ToolPolicy, req.Tools)
	return agent, nil
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
	defer restoreWorktreeOwnership(trees, workDir, log)()

	// The MCP providers a task hosts share the same model runtime its agent
	// stages run on, so a server-initiated sampling request is answered from
	// the task's own model rather than a second provider client.
	var llm *runtime.Runtime
	if len(req.MCPServers) > 0 {
		llm = agentexec.NewRuntime(req.Providers)
	}
	mcpSet, mcpErr := startMCPProviders(ctx, req.MCPServers, taskSamplingHandler(llm, req.Cfg), log)
	if mcpSet != nil {
		defer mcpSet.cleanup(ctx, log)
	}
	if mcpErr != nil {
		log.Warn("mcp providers had errors, continuing with available tools", "err", mcpErr)
	}

	agent, err := stageRunners(req, mcpSet, newRunner, llm, log)
	if err != nil {
		return nil, err
	}
	agent = persistentRunner{Runner: agent, enabled: req.Repo.PersistentStorage}

	// Forward workflow events to the daemon over NATS.
	var bus *events.Bus
	if dependencies.events != nil {
		bus = events.NewBus()
		sub := bus.Subscribe(64)
		defer sub.Close()
		go agentexec.ForwardTaskEvents(sub, dependencies.events, req.Task.ID, log)
	}

	tc := &workflow.TaskContext{
		Task:       req.Task,
		Repo:       req.Repo,
		Cfg:        req.Cfg.ToConfig(),
		Forge:      dependencies.forge,
		Store:      dependencies.store,
		Calls:      dependencies.calls,
		Trees:      trees,
		Agent:      agent,
		Bus:        bus,
		Log:        log,
		Guardrails: dependencies.guardrails,
	}
	// pr-review reads the PR data the daemon prefetched into workDir.
	if req.Task.Workflow == "pr-review" {
		tc.PRSource = prsource.NewFromMount(filepath.Join(workDir, prsource.PrefetchDirName))
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

// CompilePinnedWorkflow compiles the definition the request carries. With
// none, it pins one from the shipped definitions onto req and req.Task.
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
