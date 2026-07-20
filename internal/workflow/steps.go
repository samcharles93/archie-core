package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/samcharles93/archie-core/internal/store"
)

// Shared step library. Every workflow composes these; workflow-specific
// stages live next to their workflow definition.

// StagePrepareWorktree clones the repo fresh and checks out the task
// branch.
func StagePrepareWorktree() Stage {
	return Stage{Name: "prepare", Run: func(ctx context.Context, tc *TaskContext) error {
		dir, branch, err := tc.Trees.Prepare(ctx, tc.Task.Owner, tc.Task.Repo, tc.Repo.BaseBranch(), tc.Task.IssueNumber)
		if err != nil {
			return err
		}
		tc.Dir, tc.Branch = dir, branch
		tc.Task.Branch = branch
		return nil
	}}
}

// StageCommitPush commits everything in the worktree and pushes the
// branch; an empty tree parks (the workflow produced nothing).
func StageCommitPush(message func(*TaskContext) string) Stage {
	return Stage{Name: "commit-push", Run: func(ctx context.Context, tc *TaskContext) error {
		changed, err := tc.Trees.CommitAll(ctx, tc.Dir, message(tc))
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("worktree has no changes to commit")
		}
		return tc.Trees.Push(ctx, tc.Dir, tc.Branch)
	}}
}

// StageDiffCap parks tasks whose diff exceeds the configured line cap —
// oversized changes need human pre-approval, not an auto-opened PR.
func StageDiffCap() Stage {
	return Stage{Name: "diff-cap", Run: func(ctx context.Context, tc *TaskContext) error {
		lines, err := tc.Trees.ChangedLines(ctx, tc.Dir, tc.Repo.BaseBranch())
		if err != nil {
			return err
		}
		if lines > tc.Cfg.DiffCapLines {
			tc.Outcome = Outcome{
				Status: store.StatusParked,
				Detail: fmt.Sprintf("diff is %d changed lines (cap %d) — split the issue or approve manually", lines, tc.Cfg.DiffCapLines),
			}
		}
		return nil
	}}
}

// StageOpenPR opens the pull request with the given body and records it.
func StageOpenPR(body func(*TaskContext) string) Stage {
	return Stage{Name: "open-pr", Run: func(ctx context.Context, tc *TaskContext) error {
		t := tc.Task
		title := fmt.Sprintf("%s (archie)", t.Title)
		num, err := tc.Forge.CreatePR(ctx, t.Owner, t.Repo, title, tc.Branch, tc.Repo.BaseBranch(), body(tc))
		if err != nil {
			return err
		}
		t.PRNumber = num
		tc.Outcome = Outcome{Status: store.StatusPROpen, Detail: fmt.Sprintf("PR #%d", num)}
		return nil
	}}
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
				content := fmt.Sprintf("# archie bootstrap\n\nIssue: #%d — %s\nTime: %s\n\nThis file proves the archie pipeline (poll → worktree → push → PR) works for this repository.\n",
					tc.Task.IssueNumber, tc.Task.Title, time.Now().UTC().Format(time.RFC3339))
				return os.WriteFile(filepath.Join(dir, "bootstrap.md"), []byte(content), 0o644)
			}},
			StageCommitPush(func(tc *TaskContext) string {
				return fmt.Sprintf("chore: archie bootstrap marker for #%d", tc.Task.IssueNumber)
			}),
			StageDiffCap(),
			StageOpenPR(func(tc *TaskContext) string {
				return fmt.Sprintf("Deterministic bootstrap PR from archie's plumbing walk-through.\n\nCloses #%d", tc.Task.IssueNumber)
			}),
		},
	}
}
