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
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/domain/workflow/wfeval"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
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

func (h *hybridTrees) Diff(ctx context.Context, dir, base string) (string, error) {
	return h.local.Diff(ctx, dir, base)
}

func (h *hybridTrees) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	return h.local.ChangedFiles(ctx, dir, base)
}

func (h *hybridTrees) ChangedLines(ctx context.Context, dir, base string) (int, error) {
	return h.local.ChangedLines(ctx, dir, base)
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
	store  store.WorkflowStore
	trees  remoteTrees
	events agentexec.EventPublisher
}

type runnerFactory func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner

func newTaskRunner(providers map[string]agentexec.Provider, log *slog.Logger) agentexec.Runner {
	return agentexec.NewLoopRunner(agentexec.NewRuntime(providers), log)
}

// runTask builds a workflow.Registry from the container's mounted worktree,
// routes and runs the entire workflow, and reports its terminal outcome.
// Store remains archied's authority; Response.Task is a logging snapshot.
func runTask(ctx context.Context, req taskrun.Request, dependencies taskDependencies, newRunner runnerFactory, workDir string, log *slog.Logger) (*taskrun.Response, error) {
	registry, err := skillbuild.BuildRegistry(workDir)
	if err != nil {
		return nil, fmt.Errorf("build registry: %w", err)
	}

	wf := workflow.Route(req.Task, registry)

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

	// Start MCP providers and build a local tool registry.
	mcpSet, mcpErr := startMCPProviders(ctx, req.MCPServers, log)
	if mcpSet != nil {
		defer mcpSet.cleanup(ctx, log)
	}
	if mcpErr != nil {
		log.Warn("mcp providers had errors, continuing with available tools", "err", mcpErr)
	}

	// Build a runner with MCP-discovered tools when available.
	var agent agentexec.Runner
	if mcpSet != nil && mcpSet.registry != nil {
		agent = agentexec.NewLoopRunner(agentexec.NewRuntime(req.Providers), log, mcpSet.registry)
	} else {
		agent = newRunner(req.Providers, log)
	}
	if agent == nil {
		return nil, fmt.Errorf("no agent runner configured for task %d", req.Task.ID)
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
		Task:         req.Task,
		Repo:         req.Repo,
		Cfg:          req.Cfg.ToConfig(),
		Forge:        dependencies.forge,
		Store:        dependencies.store,
		Trees:        trees,
		Agent:        agent,
		Bus:          bus,
		Log:          log,
		CustomStages: wfeval.Discover,
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
