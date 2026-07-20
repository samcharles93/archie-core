// Package workflow is archied's extensible pipeline engine. A Workflow
// is an ordered list of stages over a shared TaskContext; stages are
// either deterministic steps (git, gate, PR, comments) or agent stages
// (agentloop runs). New workflows compose from the shared step library —
// adding one must never require reimplementing the engine or the steps.
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// TaskContext carries everything a stage may need. Stages communicate
// forward by mutating Task (persisted after every stage) and the
// scratch fields below.
type TaskContext struct {
	Task  *store.Task
	Repo  config.Repo
	Cfg   config.Config
	Forge *forge.Client
	Store *store.Store
	Trees *worktree.Manager
	Log   *slog.Logger

	// Dir/Branch are set by the prepare step.
	Dir    string
	Branch string
	// Outcome describes where the task ended up; the engine applies it.
	Outcome Outcome
}

// Outcome is a stage's terminal decision for the whole workflow. Stages
// that don't end the workflow leave it zero.
type Outcome struct {
	Status string // store.Status* value; empty = continue to next stage
	Detail string // park reason / close rationale / PR body context
}

// Stage is one named step. Returning an error parks the task with the
// error text; setting ctx.Outcome ends the workflow with that status.
type Stage struct {
	Name string
	Run  func(ctx context.Context, tc *TaskContext) error
}

// Workflow is a named, ordered stage list.
type Workflow struct {
	Name   string
	Stages []Stage
}

// Registry maps workflow names to definitions.
type Registry map[string]Workflow

// Route picks the workflow for a task. Label-driven first; an LLM
// triage stage inside a workflow can still re-route by returning an
// Outcome that re-enqueues under a different workflow (later phase).
func Route(t *store.Task, reg Registry) Workflow {
	labels := strings.Split(t.Labels, ",")
	for _, l := range labels {
		switch strings.TrimSpace(l) {
		case "bug":
			if wf, ok := reg["tdd"]; ok {
				return wf
			}
		case "feature":
			if wf, ok := reg["feasibility"]; ok {
				return wf
			}
		}
	}
	if wf, ok := reg["implement"]; ok {
		return wf
	}
	if wf, ok := reg["default"]; ok {
		return wf
	}
	// Registry always ships a default; this is a config error backstop.
	return Workflow{Name: "none", Stages: []Stage{{
		Name: "fail",
		Run: func(context.Context, *TaskContext) error {
			return fmt.Errorf("no workflow registered for task")
		},
	}}}
}

// Run executes the workflow: stages in order, task persisted after each,
// errors park the task with a comment on the issue — never silently.
func Run(ctx context.Context, wf Workflow, tc *TaskContext) {
	t := tc.Task
	t.Workflow = wf.Name
	log := tc.Log.With("workflow", wf.Name, "repo", tc.Repo.FullName(), "issue", t.IssueNumber)

	for _, stage := range wf.Stages {
		t.Stage = stage.Name
		_ = tc.Store.Update(ctx, t)
		log.Info("stage starting", "stage", stage.Name)

		if err := stage.Run(ctx, tc); err != nil {
			t.ParkReason = fmt.Sprintf("stage %s: %v", stage.Name, err)
			log.Warn("stage failed — parking", "stage", stage.Name, "err", err)
			park(ctx, tc, t.ParkReason)
			return
		}
		if tc.Outcome.Status != "" {
			finish(ctx, tc, log)
			return
		}
	}
	// A workflow must end with an explicit outcome; not doing so is a
	// definition bug, which still must not vanish silently.
	park(ctx, tc, "workflow ended without an outcome (definition bug)")
}

func finish(ctx context.Context, tc *TaskContext, log *slog.Logger) {
	t := tc.Task
	if tc.Outcome.Status == store.StatusParked {
		park(ctx, tc, tc.Outcome.Detail)
		return
	}
	_ = tc.Store.Update(ctx, t)
	_ = tc.Store.Transition(ctx, t.ID, store.StatusRunning, tc.Outcome.Status, tc.Outcome.Detail)
	log.Info("workflow finished", "status", tc.Outcome.Status)
}

func park(ctx context.Context, tc *TaskContext, reason string) {
	t := tc.Task
	t.ParkReason = reason
	_ = tc.Store.Update(ctx, t)
	_ = tc.Store.Transition(ctx, t.ID, store.StatusRunning, store.StatusParked, reason)
	body := fmt.Sprintf("**archie parked this task.**\n\n```\n%s\n```\n\nThe worktree is kept for inspection. Re-add the `%s` label after addressing this to retry.",
		clip(reason, 3000), tc.Cfg.Label)
	if err := tc.Forge.Comment(ctx, t.Owner, t.Repo, t.IssueNumber, body); err != nil {
		tc.Log.Error("failed to post park comment", "err", err)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}
