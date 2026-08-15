package agentworker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/domain/workflow/wfeval"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// hybridTrees implements workflow.Trees by splitting operations: Prepare
// and Push (network ops needing the daemon's forge credential) proxy over
// worktreerpc; CommitAll/Diff/ChangedFiles/ChangedLines are local git
// operations run directly against the container's bind-mounted worktree.
//
// Prepare deliberately discards the RPC response's directory (the
// daemon's host-side path) and returns localDir instead  --  archied and
// archie-agent see the same files at different paths.
type remoteTrees interface {
	Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error)
	Push(ctx context.Context, owner, repo string, issue int, branch string) error
}

type hybridTrees struct {
	push        remoteTrees
	local       *worktree.Manager
	owner, repo string
	issue       int
	localDir    string
}

func (h *hybridTrees) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error) {
	_, branch, err = h.push.Prepare(ctx, owner, repo, base, issue, title, body, labels)
	if err != nil {
		return "", "", err
	}
	return h.localDir, branch, nil
}

func (h *hybridTrees) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	return h.local.CommitAll(ctx, dir, message)
}

func (h *hybridTrees) Push(ctx context.Context, dir, branch string) error {
	return h.push.Push(ctx, h.owner, h.repo, h.issue, branch)
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

type taskDependencies struct {
	forge  workflow.Forger
	store  store.WorkflowStore
	trees  remoteTrees
	events agentexec.EventPublisher
}

// runTask builds a workflow.Registry from the container's mounted worktree,
// routes and runs the entire workflow, and reports its terminal outcome.
// Store remains archied's authority; Response.Task is a logging snapshot.
func runTask(ctx context.Context, req taskrun.Request, dependencies taskDependencies, newRunner agentexec.RunnerFactory, workDir string, log *slog.Logger) (*taskrun.Response, error) {
	registry, err := skillbuild.BuildRegistry(workDir)
	if err != nil {
		return nil, fmt.Errorf("build registry: %w", err)
	}

	wf := workflow.Route(req.Task, registry)

	trees := &hybridTrees{
		push:     dependencies.trees,
		local:    &worktree.Manager{WorkDir: workDir},
		owner:    req.Task.Owner,
		repo:     req.Task.Repo,
		issue:    req.Task.IssueNumber,
		localDir: workDir,
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
		agent = agentexec.NewInProcessRunner(agentexec.NewRuntime(req.Providers), log, mcpSet.registry)
	} else {
		agent = newRunner(req.Providers, log)
	}
	if agent == nil {
		return nil, fmt.Errorf("no agent runner configured for task %d", req.Task.ID)
	}

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

	workflow.Run(ctx, wf, tc)

	return &taskrun.Response{
		Task:   tc.Task,
		Status: tc.Outcome.Status,
	}, nil
}
