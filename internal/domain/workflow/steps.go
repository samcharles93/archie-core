package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gate"
	"github.com/samcharles93/archie-core/internal/gate/gateeval"
)

// Shared step library. Every workflow composes these; workflow-specific
// stages live next to their workflow definition.

// The captured_after values recorded inside a changes_captured event. They
// are part of the on-disk payload: a reader distinguishes a mid-workflow
// commit from the push that ended the run, so renaming one is a migration,
// not a rename.
const (
	capturedAfterCommit      = "commit"
	capturedAfterCommitPush  = "commit-push"
	capturedAfterBaselineFix = "baseline-fix"
	// capturedAfterOpenPR is the final capture of a run: OpenPR takes it the
	// moment the PR number is recorded, because that is the only point at which
	// a capture can carry one.
	capturedAfterOpenPR = "open-pr"
)

// changeStatsReader is the optional capability through which a Trees
// implementation reports what an attempt changed. It is deliberately
// unexported, and deliberately NOT a method on Trees: Trees is projected into
// the interpreted-stage symbol table, so declaring it there would widen what
// repository-authored .archie/stages/*.go may call (pinned by
// wfextract/reachability_test.go). A Trees implementation without it degrades
// to no capture rather than failing the stage.
type changeStatsReader interface {
	ChangedFileStats(ctx context.Context, dir, base string) (task.ChangeStats, error)
}

// captureChanges records what this attempt has changed, read off the worktree
// at the moment it was committed or pushed -- the only point where the
// worktree still holds the change and a commit or push is known to have
// happened. A retry resets the branch onto its base and a terminal state
// deletes the worktree, so nothing can re-derive this afterwards.
//
// Capturing is reporting, not work: every failure path here logs and returns,
// because a provenance record that could not be written must never park or
// fail a run that otherwise succeeded.
//
// It returns the measured stats so a caller that runs at a point the worktree's
// own revision matters (OpenPR) can reuse the read instead of paying for a
// second one -- and gets the zero value on any failure path, which is why a
// caller must treat an empty HeadSHA as "not measured" rather than as a
// revision.
func (tc *TaskContext) captureChanges(ctx context.Context, after string) task.ChangeStats {
	if tc.Dir == "" {
		tc.Log.Warn("change capture skipped: no worktree to read", "captured_after", after)
		return task.ChangeStats{}
	}
	reader, ok := tc.Trees.(changeStatsReader)
	if !ok {
		tc.Log.Warn("change capture unavailable: this worktree implementation cannot report a diffstat",
			"captured_after", after)
		return task.ChangeStats{}
	}
	stats, err := reader.ChangedFileStats(ctx, tc.Dir, tc.Repo.BaseBranch())
	if err != nil {
		tc.Log.Warn("change capture failed", "captured_after", after, "err", err)
		return task.ChangeStats{}
	}
	// Durable, never bus-only: the worktree this was measured from is gone by
	// the time anyone reads it, so a capture that only reached the live feed
	// would be lost with the process.
	if err := tc.EmitDurable(ctx, events.KindChangesCaptured, tc.Task.Stage, "", changeCaptureData(tc, after, stats)); err != nil {
		tc.Log.Warn("change capture not persisted", "captured_after", after, "err", err)
	}
	return stats
}

// changeCaptureData builds the persisted payload for one capture. Totals are
// taken over the FULL set of changed files, so a capture truncated at
// task.MaxCapturedFiles still reports how much the attempt changed -- only the
// per-file breakdown is bounded.
func changeCaptureData(tc *TaskContext, after string, stats task.ChangeStats) map[string]any {
	files := stats.Files
	truncated := false
	if len(files) > task.MaxCapturedFiles {
		files = files[:task.MaxCapturedFiles]
		truncated = true
	}
	return map[string]any{
		"schema":         events.ChangesCapturedSchema,
		"owner":          tc.Task.Owner,
		"repo":           tc.Task.Repo,
		"base":           tc.Repo.BaseBranch(),
		"branch":         tc.Branch,
		"head_sha":       stats.HeadSHA,
		"base_sha":       stats.BaseSHA,
		"pr_number":      tc.Task.PRNumber,
		"captured_after": after,
		"files":          files,
		"totals":         stats.Totals,
		"truncated":      truncated,
	}
}

// StagePrepareWorktree clones the repo fresh and checks out the task
// branch. Skips if the daemon already prepared the worktree (Docker
// containers require the tree before acquire).
func StagePrepareWorktree() Stage {
	return Stage{Name: "prepare", Run: func(ctx context.Context, tc *TaskContext) error {
		dir, branch, err := tc.Trees.Prepare(ctx, tc.Task.Owner, tc.Task.Repo, tc.Repo.BaseBranch(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Labels)
		if err != nil {
			return err
		}
		tc.Dir, tc.Branch = dir, branch
		tc.Task.Branch = branch
		return nil
	}}
}

// StageCommit commits everything in the worktree without pushing  --
// used mid-workflow so a PR tells its story in multiple commits (TDD:
// failing tests first, fix second). An empty tree parks.
func StageCommit(name string, message func(*TaskContext) string) Stage {
	return Stage{Name: name, Run: func(ctx context.Context, tc *TaskContext) error {
		changed, err := tc.Trees.CommitAll(ctx, tc.Dir, message(tc))
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("worktree has no changes to commit")
		}
		tc.captureChanges(ctx, capturedAfterCommit)
		return nil
	}}
}

// StageCommitPush commits everything in the worktree and pushes the
// branch. When the builder completed with no changes (BuildNoChanges is
// set), the issue is already resolved  --  close it with a comment instead
// of erroring on an empty tree.
//
// When StageBaselineGate already committed a real fix (BaselineFixed) and
// the build stage made nothing further, the worktree has no *new*
// uncommitted changes -- CommitAll correctly reports changed=false -- but
// there is still a real, gate-verified commit sitting on the branch that
// must reach a PR rather than being discarded with the rest of an
// abandoned worktree. Push runs regardless of changed in that case.
func StageCommitPush(message func(*TaskContext) string) Stage {
	return Stage{Name: "commit-push", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.BuildNoChanges {
			return closeNoChangesIssue(ctx, tc)
		}
		changed, err := tc.Trees.CommitAll(ctx, tc.Dir, message(tc))
		if err != nil {
			return err
		}
		if !changed && !tc.BaselineFixed {
			return fmt.Errorf("worktree has no changes to commit")
		}
		if err := tc.Trees.Push(ctx, tc.Dir, tc.Branch); err != nil {
			return err
		}
		tc.captureChanges(ctx, capturedAfterCommitPush)
		return nil
	}}
}

func closeNoChangesIssue(ctx context.Context, tc *TaskContext) error {
	if !tc.Task.IsForgeBacked() {
		tc.Outcome = Outcome{Status: StatusMerged, Detail: "completed  --  no changes required"}
		return nil
	}
	if err := tc.Forge.CloseIssue(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, ""); err != nil {
		return err
	}
	tc.Outcome = Outcome{Status: StatusMerged, Detail: "completed  --  no changes required"}
	return nil
}

// StageDiffCap parks tasks whose diff exceeds the configured line cap  --
// oversized changes need human pre-approval, not an auto-opened PR.
func StageDiffCap() Stage {
	return Stage{Name: "diff-cap", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.Cfg.DiffCapLines <= 0 {
			return nil
		}
		lines, err := tc.Trees.ChangedLines(ctx, tc.Dir, tc.Repo.BaseBranch())
		if err != nil {
			return err
		}
		if lines > tc.Cfg.DiffCapLines {
			tc.Outcome = Outcome{
				Status: StatusParked,
				Detail: fmt.Sprintf("diff is %d changed lines (cap %d)  --  split the issue or approve manually", lines, tc.Cfg.DiffCapLines),
			}
		}
		return nil
	}}
}

// StageRepoStages runs every custom stage the repo defines under
// .archie/stages/*.go (Yaegi-interpreted via TaskContext.CustomStages),
// in the order the loader returns them  --  a no-op when no loader is wired
// up or the repo defines none. A custom stage that sets tc.Outcome ends
// the workflow there, same as any built-in stage.
func StageRepoStages() Stage {
	return Stage{Name: "repo-stages", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.CustomStages == nil {
			return nil
		}
		stages, err := tc.CustomStages(tc.Dir)
		if err != nil {
			return fmt.Errorf("repo stages: %w", err)
		}
		for _, s := range stages {
			tc.Log.Info("repo stage starting", "stage", s.Name)
			if err := s.Run(ctx, tc); err != nil {
				return fmt.Errorf("repo stage %s: %w", s.Name, err)
			}
			if tc.Outcome.Status != "" {
				return nil
			}
		}
		return nil
	}}
}

// StageYaegiGate evaluates the repo's optional .archie/gate.go  --  a
// Yaegi-interpreted Go file inspecting the committed diff for
// project-specific rules shell gate commands can't express (AST checks,
// diff scanning). A missing script is a no-op. Error-level findings
// park the task; warn-level findings are logged only.
func StageYaegiGate() Stage {
	return Stage{Name: "custom-gate", Run: func(ctx context.Context, tc *TaskContext) error {
		base := tc.Repo.BaseBranch()
		diff, err := tc.Trees.Diff(ctx, tc.Dir, base)
		if err != nil {
			return fmt.Errorf("custom gate: diff against %s: %w", base, err)
		}
		files, err := tc.Trees.ChangedFiles(ctx, tc.Dir, base)
		if err != nil {
			return fmt.Errorf("custom gate: changed files against %s: %w", base, err)
		}

		findings, err := gateeval.Evaluate(gate.GateContext{
			Diff:         diff,
			ChangedFiles: files,
			Dir:          tc.Dir,
			BaseRef:      "origin/" + base,
			Repo:         tc.Repo.FullName(),
		})
		if err != nil {
			return fmt.Errorf("custom gate: %w", err)
		}

		var blocking []string
		for _, f := range findings {
			if err := f.Validate(); err != nil {
				return fmt.Errorf("custom gate: %w", err)
			}
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			msg := strings.TrimSpace(loc + ": " + f.Message)
			if f.Blocking() {
				blocking = append(blocking, msg)
			}
			tc.Log.Info("custom gate finding", "level", f.Level, "file", f.File, "line", f.Line, "message", f.Message)
		}
		if len(blocking) > 0 {
			return fmt.Errorf("custom gate: %s", strings.Join(blocking, "; "))
		}
		return nil
	}}
}

// OpenPR opens the task's pull request, records its number, and sets
// the terminal pr_open outcome. Stages that need to act after the PR
// exists (e.g. posting evidence comments) call this and then do so in
// the same stage  --  the engine stops at the first stage with an outcome.
func OpenPR(ctx context.Context, tc *TaskContext, body string) error {
	t := tc.Task
	title := fmt.Sprintf("%s (archie)", t.Title)
	if t.IsForgeBacked() {
		// Best-effort. The link is cosmetic -- it puts the branch in the
		// issue's sidebar on Gitea and does nothing on GitHub -- so failing
		// the stage on it meant the pull request, the entire point of the
		// run, was never opened and the task parked. A retry then re-links
		// the same branch, which Gitea answers with a conflict, so every
		// retry parked again.
		if err := tc.Forge.LinkBranch(ctx, t.Owner, t.Repo, t.IssueNumber, tc.Branch); err != nil {
			if tc.Log != nil {
				tc.Log.Warn("link branch to issue failed; opening the PR anyway",
					"repo", t.Owner+"/"+t.Repo, "issue", t.IssueNumber,
					"branch", tc.Branch, "err", err)
			}
		}
	}
	num, err := tc.Forge.CreatePR(ctx, t.Owner, t.Repo, title, tc.Branch, tc.Repo.BaseBranch(), body)
	if err != nil {
		return err
	}
	t.PRNumber = num
	tc.Outcome = Outcome{Status: StatusPROpen, Detail: fmt.Sprintf("PR #%d", num)}
	// The captures taken while committing and pushing ran before this PR
	// existed, so they carry no number and the changed-files view cannot link
	// it. This is the one point where the number is known and the worktree is
	// still there to measure, so it is captured here rather than left for a
	// read to reconstruct. Reporting only: captureChanges never fails the stage.
	//
	// The same read answers which revision this run's line numbers describe:
	// nothing commits between StageReview and here, so the worktree still holds
	// the head the review read, and the stage that posts line-anchored comments
	// needs it to refuse a post once the PR's head has moved past it.
	if stats := tc.captureChanges(ctx, capturedAfterOpenPR); stats.HeadSHA != "" {
		tc.ReviewedHeadSHA = stats.HeadSHA
	}
	return nil
}

// StageOpenPR opens the pull request with the given body and records it.
func StageOpenPR(body func(*TaskContext) string) Stage {
	return Stage{Name: "open-pr", Run: func(ctx context.Context, tc *TaskContext) error {
		return OpenPR(ctx, tc, body(tc))
	}}
}

func taskPromptBlock(task *Task) string {
	if task.IsForgeBacked() {
		return fmt.Sprintf("<issue number=%d>\n# %s\n\n%s\n</issue>",
			task.IssueNumber, task.Title, task.Body)
	}
	return fmt.Sprintf("<task source=\"chat\">\n# %s\n\n%s\n</task>", task.Title, task.Body)
}

func taskKind(task *Task) string {
	if task.IsForgeBacked() {
		return "GitHub issue"
	}
	return "chat-originated task"
}

func commitIssueReference(verb string, task *Task) string {
	if !task.IsForgeBacked() {
		return ""
	}
	return fmt.Sprintf("\n\n%s #%d", verb, task.IssueNumber)
}

// Bootstrap is the deterministic no-LLM workflow that proves the
// plumbing: it adds a marker file and opens a PR referencing the issue.
// It stays registered as a diagnostics workflow (label a test issue and
// you exercise invites, clone, push, and PR mechanics end to end).
func Bootstrap() Workflow {
	return Workflow{
		Name: "bootstrap",
		Stages: []Stage{
			StagePrepareWorktree(),
			{Name: "apply", Run: func(ctx context.Context, tc *TaskContext) error {
				dir := filepath.Join(tc.Dir, ".archie")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				content := fmt.Sprintf("# archie bootstrap\n\n%s\nTime: %s\n\nThis file proves the archie pipeline (queue → worktree → push → PR) works for this repository.\n",
					taskPromptBlock(tc.Task), time.Now().UTC().Format(time.RFC3339))
				return os.WriteFile(filepath.Join(dir, "bootstrap.md"), []byte(content), 0o644)
			}},
			StageCommitPush(func(tc *TaskContext) string {
				return "chore: archie bootstrap marker" + commitIssueReference("Refs", tc.Task)
			}),
			StageDiffCap(),
			StageOpenPR(func(tc *TaskContext) string {
				return "Deterministic bootstrap PR from archie's plumbing walk-through."
			}),
		},
	}
}
