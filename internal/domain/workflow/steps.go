package workflow

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
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

// changeStatsReader reports what an attempt changed. Trees without it skip
// capture.
type changeStatsReader interface {
	ChangedFileStats(ctx context.Context, dir, base string) (task.ChangeStats, error)
}

// captureChanges records what this attempt changed, at commit or push time.
// Failures are logged and never fail the run; they return the zero value, so
// an empty HeadSHA means not measured.
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
	if err := tc.EmitDurable(ctx, events.KindChangesCaptured, tc.Stage, "", changeCaptureData(tc, after, stats)); err != nil {
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

// StagePrepareWorktree binds the task's worktree for a fresh run: it positions
// it onto the repository's base branch. Skips if the daemon already prepared
// the worktree (Docker containers require the tree before acquire); in a
// container the adapter resolves the directory the daemon prepared.
func StagePrepareWorktree() Stage {
	return prepareWorktreeStage("prepare", func(*TaskContext) (PrepareTarget, error) {
		return PrepareFresh, nil
	})
}

// StagePrepareWorktreeOnBranch binds tc.Dir and tc.Branch to the worktree the
// daemon positioned on the task's persisted PR branch. An empty branch fails.
func StagePrepareWorktreeOnBranch() Stage {
	return prepareWorktreeStage("prepare", func(tc *TaskContext) (PrepareTarget, error) {
		if tc.Task.Branch == "" {
			return "", fmt.Errorf("prepare: remediate task has no branch to resume")
		}
		return PrepareTarget(tc.Task.Branch), nil
	})
}

// prepareWorktreeStage builds a stage that prepares the task's worktree at the
// target its workflow selects and binds the result onto the task context.
func prepareWorktreeStage(name string, target func(*TaskContext) (PrepareTarget, error)) Stage {
	return Stage{Name: name, Run: func(ctx context.Context, tc *TaskContext) error {
		t, err := target(tc)
		if err != nil {
			return err
		}
		dir, branch, err := tc.Trees.Prepare(ctx, tc.Task.Owner, tc.Task.Repo, tc.Repo.BaseBranch(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Labels, t)
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

// StageCommitPush commits the worktree and pushes the branch. With no changes
// it closes the issue with a comment, unless a baseline fix was committed, in
// which case it still pushes.
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
		tc.Outcome = Outcome{Status: StatusCompleted, Detail: "completed  --  no changes required"}
		return nil
	}
	if err := tc.Forge.CloseIssue(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, ""); err != nil {
		return err
	}
	tc.Outcome = Outcome{Status: StatusCompleted, Detail: "completed  --  no changes required"}
	return nil
}

// StageDiffCap parks tasks whose diff exceeds the configured line cap  --
// oversized changes need human pre-approval, not an auto-opened PR.
func StageDiffCap() Stage {
	return Stage{Name: "diff-cap", Run: func(ctx context.Context, tc *TaskContext) error {
		capLines := tc.Cfg.DiffCap()
		if capLines <= 0 {
			return nil
		}
		lines, err := tc.Trees.ChangedLines(ctx, tc.Dir, tc.Repo.BaseBranch())
		if err != nil {
			return err
		}
		if lines > capLines {
			// No "approve" here: a parked task's operator actions are retry,
			// abandon and reject (internal/taskstate), so telling the operator
			// to approve sent them looking for a button that does not exist.
			tc.Outcome = Outcome{
				Status: StatusParked,
				Detail: fmt.Sprintf("diff is %d changed lines (cap %d)  --  split the issue, or raise diff_cap_lines and retry", lines, capLines),
			}
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
		// Best-effort: the link is cosmetic.
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
	// Capture again now that the PR number is known.
	tc.captureChanges(ctx, capturedAfterOpenPR)
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
