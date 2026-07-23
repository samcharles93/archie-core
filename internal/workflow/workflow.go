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
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/skill"
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
	Forge forge.Forge
	Store *store.Store
	Trees *worktree.Manager
	Agent agentexec.Runner
	Bus   *events.Bus // nil-safe via Emit
	Log   *slog.Logger
	// CustomStages discovers a repo's per-repo custom stages (Yaegi-
	// interpreted from .archie/stages/*.go in the given worktree
	// directory), returning them in the order they should run. Wired up
	// by the composition root (cmd/archied) — nil disables discovery, so
	// StageRepoStages is a no-op. Kept as an injected function rather
	// than a direct import to avoid workflow depending on its own
	// generated Yaegi symbol table (an import cycle).
	CustomStages func(dir string) ([]Stage, error)

	// SkillBody is the Markdown body of the loaded SKILL.md for this
	// workflow's matching skill, injected into agent context. Empty
	// when no skill is found (backward compatible).
	SkillBody string
	// SkillPlugins holds the bundled Yaegi plugins loaded from the
	// skill's plugins/ directory. Populated by loadSkillBody alongside
	// SkillBody. Nil when no skill is loaded or the skill has no plugins.
	SkillPlugins []skill.Plugin
	// Dir/Branch are set by the prepare step.
	Dir    string
	Branch string
	// BuildSummary is the builder agent's finish summary — the PR body.
	BuildSummary string
	// BuildNoChanges is set when the builder returned StatusPassed but
	// made no file changes — the fix already exists or the issue is a
	// no-op. StageCommitPush closes the issue instead of erroring.
	BuildNoChanges bool
	// ReproProof is the captured failing-test output from a TDD repro
	// stage, posted on the PR as evidence the bug was reproduced.
	ReproProof string
	// decision is the feasibility assess stage's verdict.
	decision *decision
	// Outcome describes where the task ended up; the engine applies it.
	Outcome Outcome
}

// Emit publishes an observability event stamped with the task's
// identity. Safe on a nil bus.
func (tc *TaskContext) Emit(kind, stage, detail string, data map[string]any) {
	if tc.Bus == nil {
		return
	}
	tc.Bus.Publish(events.Event{
		Kind:     kind,
		TaskID:   tc.Task.ID,
		Repo:     tc.Task.Owner + "/" + tc.Task.Repo,
		Issue:    tc.Task.IssueNumber,
		Workflow: tc.Task.Workflow,
		Stage:    stage,
		Detail:   detail,
		Data:     data,
	})
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

// Route picks the workflow for a task. A pre-assigned workflow wins
// (the waiting_human → approved handoff requeues under "implement");
// otherwise labels decide, then the default.
func Route(t *store.Task, reg Registry) Workflow {
	if t.Workflow != "" {
		if wf, ok := reg[t.Workflow]; ok {
			return wf
		}
	}
	labels := strings.SplitSeq(t.Labels, ",")
	for l := range labels {
		switch strings.TrimSpace(l) {
		case "bug":
			if wf, ok := reg["tdd"]; ok {
				return wf
			}
		case "feature":
			if wf, ok := reg["feasibility"]; ok {
				return wf
			}
		case "bootstrap":
			// Diagnostics: exercise the full pipeline deterministically
			// (no LLM spend) — invites, clone, push, PR, labels.
			if wf, ok := reg["bootstrap"]; ok {
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
		tc.Emit(events.KindStageStart, stage.Name, "", nil)
		started := time.Now()

		err := stage.Run(ctx, tc)
		data := map[string]any{"duration_ms": time.Since(started).Milliseconds()}
		if err != nil {
			data["error"] = err.Error()
		}
		tc.Emit(events.KindStageFinish, stage.Name, "", data)

		if err != nil {
			// Daemon shutdown is not a workflow failure. Leave the task running
			// so Startup's existing crash recovery requeues it; parking here
			// would publish a false failure and require manual intervention.
			if ctx.Err() != nil {
				log.Info("stage interrupted", "stage", stage.Name, "err", err)
				return
			}
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
	tc.Forge.SetStateLabel(ctx, t.Owner, t.Repo, t.IssueNumber, stateLabelFor(tc.Cfg.Dispatch, tc.Outcome.Status), tc.Cfg.Dispatch.LabelValues())
	tc.Emit(events.KindOutcome, t.Stage, tc.Outcome.Detail, map[string]any{"status": tc.Outcome.Status})
	log.Info("workflow finished", "status", tc.Outcome.Status)
}

// stateLabelFor maps a terminal workflow status to its forge label by
// looking up the configured [dispatch.labels]; empty clears (done states
// — the issue closes or the PR speaks).
func stateLabelFor(d config.Dispatch, status string) string {
	switch status {
	case store.StatusPROpen:
		return d.StateLabel("pr")
	case store.StatusWaitingHuman:
		return d.StateLabel("waiting")
	case store.StatusParked:
		return d.StateLabel("parked")
	case store.StatusDead:
		return d.StateLabel("dead")
	default:
		return ""
	}
}

func park(ctx context.Context, tc *TaskContext, reason string) {
	t := tc.Task
	t.ParkReason = reason
	_ = tc.Store.Update(ctx, t)
	_ = tc.Store.Transition(ctx, t.ID, store.StatusRunning, store.StatusParked, reason)
	tc.Forge.SetStateLabel(ctx, t.Owner, t.Repo, t.IssueNumber, tc.Cfg.Dispatch.StateLabel("parked"), tc.Cfg.Dispatch.LabelValues())
	tc.Emit(events.KindParked, t.Stage, reason, nil)
	body := fmt.Sprintf("**archie parked this task.**\n\n```\n%s\n```\n\nThe worktree is kept for inspection.",
		clip(reason, 3000))
	if _, err := tc.Forge.Comment(ctx, t.Owner, t.Repo, t.IssueNumber, body); err != nil {
		tc.Log.Error("failed to post park comment", "err", err)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}
