package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reviewer runs an adversarial review of a code snapshot in its own
// isolated context and reports what it found. Implementations must give
// the reviewer no path to the implementer's conversation, credentials, or
// git history -- see docs/prds/adversarial-self-review.md. A reviewer that
// errors, is truncated, or reaches no conclusion returns a ReviewReport
// with Status == ReviewStatusNotRun rather than a Go error: the stage
// always has a report to act on, and "did not run" is a first-class
// outcome, not a special case.
type Reviewer interface {
	Review(ctx context.Context, req ReviewRequest) ReviewReport
}

// ReviewRequest is everything an adversarial reviewer is given: a
// .git-free snapshot of the reviewed commit's tracked files, the diff that
// produced it, and the originating issue text. Nothing else -- commit
// messages, branch name, reflog, and the implementer's own transcript are
// structurally excluded, not merely withheld by instruction.
type ReviewRequest struct {
	// SnapshotDir is a filesystem directory containing HEAD's tracked
	// files with no .git present.
	SnapshotDir string
	Diff        string
	IssueText   string
	// MaxSteps bounds the reviewer's tool-loop iterations. 0 means the
	// implementation's own default.
	MaxSteps int
}

// reviewDetailBytes bounds how much of a park Detail the rendered review
// findings occupy, matching the store's own park-reason cap headroom (see
// baselineParkOutputBytes in implement.go).
const reviewDetailBytes = 4000

// StageReview runs the adversarial self-review stage. It is a no-op unless
// Repo.ReviewEnabled is true. A surviving confirmed error-level finding, or
// a review that failed to run at all, parks the task (StatusParked)
// with the findings in Detail and stops the workflow before StageOpenPR;
// a clean pass, or only warn/plausible findings, leaves Outcome unset so
// the workflow proceeds.
func StageReview() Stage {
	return Stage{Name: "review", Run: func(ctx context.Context, tc *TaskContext) error {
		if !tc.Repo.ReviewEnabled {
			return nil
		}
		if tc.Reviewer == nil {
			tc.Outcome = Outcome{
				Status: StatusParked,
				Detail: "adversarial review is enabled (review_enabled) but no reviewer is configured",
			}
			return nil
		}

		report, err := runReview(ctx, tc)
		if err != nil {
			return fmt.Errorf("review: %w", err)
		}
		// Stash the report regardless of outcome: the PR-body findings
		// section (h019.6) needs it when the review passes, and the parked
		// task's park reason already carries it when it does not.
		tc.ReviewReport = report
		if !report.Passed() {
			tc.Outcome = Outcome{Status: StatusParked, Detail: renderReviewDetail(report)}
		}
		return nil
	}}
}

// runReview builds the reviewer's isolated snapshot, diff and issue text,
// invokes tc.Reviewer, and cleans up the snapshot regardless of outcome.
func runReview(ctx context.Context, tc *TaskContext) (ReviewReport, error) {
	diff, err := tc.Trees.Diff(ctx, tc.Dir, tc.Repo.BaseBranch())
	if err != nil {
		return ReviewReport{}, fmt.Errorf("compute diff: %w", err)
	}

	// Beside, not inside, the task worktree: a snapshot inside tc.Dir
	// would itself be an untracked, .git-adjacent directory the next
	// commit or gate run could pick up.
	snapshotDir, err := os.MkdirTemp(filepath.Dir(tc.Dir), "review-snapshot-*")
	if err != nil {
		return ReviewReport{}, fmt.Errorf("create review snapshot directory: %w", err)
	}
	defer os.RemoveAll(snapshotDir)

	if err := tc.Trees.Snapshot(ctx, tc.Dir, snapshotDir); err != nil {
		return ReviewReport{}, fmt.Errorf("snapshot worktree for review: %w", err)
	}

	req := ReviewRequest{
		SnapshotDir: snapshotDir,
		Diff:        diff,
		IssueText:   taskPromptBlock(tc.Task),
		MaxSteps:    tc.Cfg.Budgets.MaxSteps,
	}
	return tc.Reviewer.Review(ctx, req), nil
}

// renderReviewDetail formats a review report as a park Detail: why the
// task stopped, and what a human (or a retry) needs to know.
func renderReviewDetail(report ReviewReport) string {
	if !report.Ran() {
		reason := report.SkipReason
		if reason == "" {
			reason = "no reason given"
		}
		return clip("adversarial review did not run  --  "+reason, reviewDetailBytes)
	}
	var b strings.Builder
	b.WriteString("adversarial review found blocking defects:\n")
	for _, f := range report.Findings {
		if !f.Blocking() {
			continue
		}
		fmt.Fprintf(&b, "- %s:%d %s (%s)\n", f.File, f.Line, f.Defect, f.FailureScenario)
	}
	return clip(b.String(), reviewDetailBytes)
}

// renderPRReviewSection renders the PR-body findings section (h019.6). It
// returns "" when the review did not run, so a review that was disabled or
// never wired does not claim one ran -- silence would be indistinguishable
// from the review running and finding nothing. When it did run, the section
// states the outcome explicitly, including the zero-findings case.
func renderPRReviewSection(report ReviewReport) string {
	if !report.Ran() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Adversarial review\n\n")
	if len(report.Findings) == 0 {
		b.WriteString("Adversarial self-review ran on the committed diff and found no defects.\n")
	} else {
		b.WriteString("Adversarial self-review ran and found no blocking defects. Non-blocking notes:\n\n")
		for _, f := range report.Findings {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Fprintf(&b, "- `%s` (%s, %s): %s", loc, f.Verdict, f.Level, f.Defect)
			if f.FailureScenario != "" {
				fmt.Fprintf(&b, " — %s", f.FailureScenario)
			}
			b.WriteString("\n")
		}
	}
	// The cleared properties are what back "ran and found nothing": without
	// them a zero-finding review reads the same as one that never looked
	// (h019.5 calibration).
	if len(report.Checked) > 0 {
		b.WriteString("\nChecked and cleared:\n\n")
		for _, c := range report.Checked {
			fmt.Fprintf(&b, "- **%s** — %s\n", c.Property, c.Evidence)
		}
	}
	if report.Summary != "" {
		fmt.Fprintf(&b, "\nSummary: %s\n", report.Summary)
	}
	return b.String()
}
