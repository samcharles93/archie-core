package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/gate"
	"github.com/samcharles93/archie-core/internal/gate/gateeval"
	"github.com/samcharles93/archie-core/internal/store"
)

// Shared step library. Every workflow composes these; workflow-specific
// stages live next to their workflow definition.

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
		return nil
	}}
}

// StageCommitPush commits everything in the worktree and pushes the
// branch. When the builder completed with no changes (BuildNoChanges is
// set), the issue is already resolved  --  close it with a comment instead
// of erroring on an empty tree.
func StageCommitPush(message func(*TaskContext) string) Stage {
	return Stage{Name: "commit-push", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.BuildNoChanges {
			if tc.Task.IsForgeBacked() {
				body := fmt.Sprintf("**archie closed this issue  --  no changes required.**\n\n%s", tc.BuildSummary)
				if _, err := tc.Forge.Comment(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, body); err != nil {
					tc.Log.Warn("no-op close comment failed", "err", err)
				}
				if err := tc.Forge.CloseIssue(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, ""); err != nil {
					return err
				}
			}
			tc.Outcome = Outcome{Status: store.StatusMerged, Detail: "completed  --  no changes required"}
			return nil
		}
		commit := StageCommit("commit-push", message)
		if err := commit.Run(ctx, tc); err != nil {
			return err
		}
		return tc.Trees.Push(ctx, tc.Dir, tc.Branch)
	}}
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
				Status: store.StatusParked,
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
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			msg := strings.TrimSpace(loc + ": " + f.Message)
			if f.Level == "error" {
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
	num, err := tc.Forge.CreatePR(ctx, t.Owner, t.Repo, title, tc.Branch, tc.Repo.BaseBranch(), body)
	if err != nil {
		return err
	}
	t.PRNumber = num
	tc.Outcome = Outcome{Status: store.StatusPROpen, Detail: fmt.Sprintf("PR #%d", num)}
	return nil
}

// StageOpenPR opens the pull request with the given body and records it.
func StageOpenPR(body func(*TaskContext) string) Stage {
	return Stage{Name: "open-pr", Run: func(ctx context.Context, tc *TaskContext) error {
		return OpenPR(ctx, tc, body(tc))
	}}
}

func issueClosureReference(task *store.Task) string {
	if !task.IsForgeBacked() {
		return ""
	}
	return fmt.Sprintf("\n\nCloses #%d", task.IssueNumber)
}

func taskPromptBlock(task *store.Task) string {
	if task.IsForgeBacked() {
		return fmt.Sprintf("<issue number=%d>\n# %s\n\n%s\n</issue>",
			task.IssueNumber, task.Title, task.Body)
	}
	return fmt.Sprintf("<task source=\"chat\">\n# %s\n\n%s\n</task>", task.Title, task.Body)
}

func taskKind(task *store.Task) string {
	if task.IsForgeBacked() {
		return "GitHub issue"
	}
	return "chat-originated task"
}

func commitIssueReference(verb string, task *store.Task) string {
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
				return "Deterministic bootstrap PR from archie's plumbing walk-through." +
					issueClosureReference(tc.Task)
			}),
		},
	}
}
