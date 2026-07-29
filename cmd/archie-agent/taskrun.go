package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/workflow/wfeval"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

const rpcTimeout = 60 * time.Second

// hybridTrees implements workflow.Trees by splitting operations: Prepare
// and Push (network ops needing the daemon's forge credential) proxy over
// worktreerpc; CommitAll/Diff/ChangedFiles/ChangedLines are local git
// operations run directly against the container's bind-mounted worktree.
//
// Prepare deliberately discards the RPC response's directory (the
// daemon's host-side path) and returns localDir instead  --  archied and
// archie-agent see the same files at different paths.
type hybridTrees struct {
	push        *worktreerpc.Client
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

// runTask builds a workflow.Registry from the container's own mounted
// worktree, routes and runs req.Task's entire workflow, and reports the
// terminal outcome. Store/Forge/worktree-push calls proxy back to archied
// over nc; Store is archied's sole authority for task state  --  the
// returned Response.Task is a best-effort snapshot for logging only.
func runTask(ctx context.Context, req taskrun.Request, nc *natsio.Conn, newRunner agentexec.RunnerFactory, workDir string, log *slog.Logger) (*taskrun.Response, error) {
	registry, err := skillbuild.BuildRegistry(workDir)
	if err != nil {
		return nil, fmt.Errorf("build registry: %w", err)
	}

	wf := workflow.Route(req.Task, registry)

	trees := &hybridTrees{
		push:     &worktreerpc.Client{Conn: nc, Timeout: rpcTimeout},
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

	tc := &workflow.TaskContext{
		Task:         req.Task,
		Repo:         req.Repo,
		Cfg:          req.Cfg.ToConfig(),
		Forge:        &forgerpc.Client{Conn: nc, Timeout: rpcTimeout},
		Store:        &storerpc.Client{Conn: nc, Timeout: rpcTimeout},
		Trees:        trees,
		Agent:        agent,
		Log:          log,
		CustomStages: wfeval.Discover,
	}

	workflow.Run(ctx, wf, tc)

	return &taskrun.Response{
		Task:   tc.Task,
		Status: tc.Outcome.Status,
	}, nil
}

// handleTaskRun decodes a taskrun.Request from msg, runs it via runTask
// against the container's fixed worktree mount, and replies with the
// outcome. Errors are reported in the reply, not returned  --  there's no
// caller to return them to; this is a NATS subscription callback.
func handleTaskRun(ctx context.Context, msg *natsio.Msg, nc *natsio.Conn, log *slog.Logger) {
	var req taskrun.Request
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		log.Error("taskrun decode failed", "err", err)
		respondTaskRun(msg, nil, fmt.Errorf("decode taskrun request: %w", err), log)
		return
	}

	log.Info("running task", "task", req.Task.ID, "repo", req.Repo.FullName(), "issue", req.Task.IssueNumber)

	resp, err := runTask(ctx, req, nc, agentexec.DefaultRunnerFactory, storage.WorktreeMountDir, log)
	if err != nil {
		log.Error("task run failed", "task", req.Task.ID, "err", err)
		respondTaskRun(msg, nil, err, log)
		return
	}

	log.Info("task run complete", "task", req.Task.ID, "status", resp.Status)
	respondTaskRun(msg, resp, nil, log)
}

func respondTaskRun(msg *natsio.Msg, resp *taskrun.Response, runErr error, log *slog.Logger) {
	if resp == nil {
		resp = &taskrun.Response{}
	}
	if runErr != nil {
		resp.Error = runErr.Error()
	}
	data, err := json.Marshal(resp)
	if err != nil {
		log.Error("taskrun encode response failed", "err", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		log.Error("taskrun respond failed", "err", err)
	}
}
