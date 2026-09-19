package workflow

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestStagePostReviewCommentsPostsLineAnchoredFindings is the q9au stage
// contract: a line-anchored finding reaches the forge as an inline comment,
// a whole-file finding does not (it has no line to attach to and stays in the
// PR body), and only a confirmed defect carries the one-click suggestion fence.
func TestStagePostReviewCommentsPostsLineAnchoredFindings(t *testing.T) {
	tests := []struct {
		name           string
		finding        ReviewFinding
		wantPosted     bool
		wantSuggestion bool
		wantBodyHas    []string
		wantBodyHasNot []string
	}{
		{
			name: "a confirmed defect carries its suggestion",
			finding: ReviewFinding{
				File: "a.go", Line: 12, Defect: "nil deref on an empty map",
				FailureScenario: "an issue with no labels panics the poller",
				Verdict:         ReviewVerdictConfirmed, Level: ReviewLevelWarn,
				// Single-line only: a multi-line suggestion is rejected by
				// ReviewFinding.Validate (Issue 2), so the posted fence replaces
				// the anchored line exactly.
				Suggestion: "if len(labels) == 0 { return nil }",
			},
			wantPosted:     true,
			wantSuggestion: true,
			wantBodyHas:    []string{"nil deref on an empty map", "an issue with no labels panics the poller", "confirmed"},
		},
		{
			name: "a plausible worry never carries one, even when supplied",
			finding: ReviewFinding{
				File: "b.go", Line: 3, Defect: "possible race",
				FailureScenario: "two writers", Verdict: ReviewVerdictPlausible,
				Level: ReviewLevelWarn, Suggestion: "mu.Lock()",
			},
			wantPosted:     true,
			wantSuggestion: false,
			wantBodyHas:    []string{"possible race", "plausible"},
			wantBodyHasNot: []string{"```suggestion", "mu.Lock()"},
		},
		{
			name: "a confirmed defect with no mechanical fix has no fence",
			finding: ReviewFinding{
				File: "c.go", Line: 9, Defect: "wrong ordering",
				FailureScenario: "retry drops the newest event",
				Verdict:         ReviewVerdictConfirmed, Level: ReviewLevelError,
			},
			wantPosted:     true,
			wantSuggestion: false,
			wantBodyHasNot: []string{"```suggestion"},
		},
		{
			name: "a whole-file finding is not posted inline",
			finding: ReviewFinding{
				File: "README.md", Line: 0, Defect: "docs describe the old topology",
				Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn,
			},
			wantPosted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeForge{}
			tc := reviewCommentContext(f, ReviewReport{
				Status:   ReviewStatusCompleted,
				Findings: []ReviewFinding{tt.finding},
			})

			if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
				t.Fatalf("stage returned %v; a posting failure must never fail the task", err)
			}
			if got := len(f.reviewComments); (got > 0) != tt.wantPosted {
				t.Fatalf("posted %d comments, want posted=%v", got, tt.wantPosted)
			}
			if !tt.wantPosted {
				return
			}
			got := f.reviewComments[0]
			if got.Path != tt.finding.File || got.Line != tt.finding.Line {
				t.Errorf("anchor = %s:%d, want %s:%d", got.Path, got.Line, tt.finding.File, tt.finding.Line)
			}
			for _, want := range tt.wantBodyHas {
				if !strings.Contains(got.Body, want) {
					t.Errorf("body %q does not mention %q", got.Body, want)
				}
			}
			for _, unwanted := range tt.wantBodyHasNot {
				if strings.Contains(got.Body, unwanted) {
					t.Errorf("body %q contains %q, which must not be offered", got.Body, unwanted)
				}
			}
			hasFence := strings.Contains(got.Body, "```suggestion")
			if hasFence != tt.wantSuggestion {
				t.Errorf("suggestion fence present = %v, want %v (body %q)", hasFence, tt.wantSuggestion, got.Body)
			}
		})
	}
}

// TestStagePostReviewCommentsPostsEveryAnchoredFindingInOneCall pins the batch
// shape: Gitea carries inline comments only on a submitted review, so the set
// must travel as one call rather than one call per finding.
func TestStagePostReviewCommentsPostsEveryAnchoredFindingInOneCall(t *testing.T) {
	f := &fakeForge{}
	tc := reviewCommentContext(f, ReviewReport{
		Status: ReviewStatusCompleted,
		Findings: []ReviewFinding{
			{File: "a.go", Line: 1, Defect: "one", Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn},
			{File: "b.go", Line: 2, Defect: "two", Verdict: ReviewVerdictPlausible, Level: ReviewLevelWarn},
			{File: "c.go", Line: 0, Defect: "file-level", Verdict: ReviewVerdictPlausible, Level: ReviewLevelWarn},
		},
	})

	if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if f.reviewCalls != 1 {
		t.Errorf("CreateReviewComments called %d times, want 1", f.reviewCalls)
	}
	if len(f.reviewComments) != 2 {
		t.Errorf("posted %d comments, want the 2 anchored findings", len(f.reviewComments))
	}
}

// TestStagePostReviewCommentsIsANoOpWithoutAReview keeps the stage silent when
// there is nothing to post: a disabled or never-wired review must not reach the
// forge at all, and a clean review has nothing to anchor.
func TestStagePostReviewCommentsIsANoOpWithoutAReview(t *testing.T) {
	tests := []struct {
		name   string
		report ReviewReport
	}{
		{name: "the review never ran", report: ReviewReport{}},
		{name: "the review was skipped", report: ReviewReport{Status: ReviewStatusSkipped, SkipReason: "no reviewer"}},
		{name: "the review ran and found nothing", report: ReviewReport{Status: ReviewStatusCompleted}},
		{
			name: "the review found only a whole-file finding",
			report: ReviewReport{Status: ReviewStatusCompleted, Findings: []ReviewFinding{
				{File: "README.md", Line: 0, Defect: "stale", Verdict: ReviewVerdictPlausible, Level: ReviewLevelWarn},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeForge{}
			tc := reviewCommentContext(f, tt.report)
			if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
				t.Fatal(err)
			}
			if f.reviewCalls != 0 {
				t.Errorf("CreateReviewComments called %d times, want 0", f.reviewCalls)
			}
		})
	}
}

// TestStagePostReviewCommentsSurvivesAPostingFailure is the "best effort"
// clause: the PR is already open and its body already lists every finding, so a
// failed comment must be logged, not turned into a parked task.
func TestStagePostReviewCommentsSurvivesAPostingFailure(t *testing.T) {
	f := &fakeForge{reviewErr: errors.New("422 line is not part of the diff")}
	tc := reviewCommentContext(f, ReviewReport{
		Status: ReviewStatusCompleted,
		Findings: []ReviewFinding{
			{File: "a.go", Line: 4, Defect: "nil deref", Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn},
		},
	})

	if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
		t.Fatalf("stage returned %v; a posting failure must never fail the task", err)
	}
	if tc.Outcome.Status != "" {
		t.Errorf("outcome = %q, want unset: a failed comment must not end the workflow", tc.Outcome.Status)
	}
}

// TestStagePostReviewCommentsNeedsAPullRequestNumber covers the one precondition
// the stage cannot invent. A task with no recorded PR number has nothing to
// anchor to, and guessing would attach the findings to unrelated work.
func TestStagePostReviewCommentsNeedsAPullRequestNumber(t *testing.T) {
	f := &fakeForge{}
	tc := reviewCommentContext(f, ReviewReport{
		Status: ReviewStatusCompleted,
		Findings: []ReviewFinding{
			{File: "a.go", Line: 4, Defect: "nil deref", Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn},
		},
	})
	tc.Task.PRNumber = 0

	if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if f.reviewCalls != 0 {
		t.Errorf("CreateReviewComments called %d times, want 0 with no PR number", f.reviewCalls)
	}
}

// TestStagePostReviewCommentsAddressesTheRecordedPullRequest pins that the
// comment lands on the task's own PR, in the task's own repo.
func TestStagePostReviewCommentsAddressesTheRecordedPullRequest(t *testing.T) {
	f := &fakeForge{}
	tc := reviewCommentContext(f, ReviewReport{
		Status: ReviewStatusCompleted,
		Findings: []ReviewFinding{
			{File: "a.go", Line: 4, Defect: "nil deref", Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn},
		},
	})

	if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if f.reviewOwner != "acme" || f.reviewRepo != "widget" || f.reviewNumber != 42 {
		t.Errorf("anchored at %s/%s#%d, want acme/widget#42", f.reviewOwner, f.reviewRepo, f.reviewNumber)
	}
}

// TestStagePostReviewCommentsThreadsTheReviewedHeadSHA is the producer half of
// the head-drift guard: the forge can only refuse a set whose line numbers
// describe a revision the pull request has left if the stage tells it which
// revision those numbers were measured on.
func TestStagePostReviewCommentsThreadsTheReviewedHeadSHA(t *testing.T) {
	tests := []struct {
		name     string
		reviewed string
	}{
		{name: "a measured revision reaches the forge", reviewed: "1a2b3c4d"},
		{
			name: "an unmeasured revision is passed through as empty so the forge posts unverified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeForge{}
			tc := reviewCommentContext(f, ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{File: "a.go", Line: 4, Defect: "nil deref", Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn},
				},
			})
			tc.ReviewedHeadSHA = tt.reviewed

			if err := StagePostReviewComments().Run(context.Background(), tc); err != nil {
				t.Fatal(err)
			}
			if f.reviewHeadSHA != tt.reviewed {
				t.Errorf("forge was handed reviewed head %q, want %q", f.reviewHeadSHA, tt.reviewed)
			}
		})
	}
}

func reviewCommentContext(f *fakeForge, report ReviewReport) *TaskContext {
	return &TaskContext{
		Task:         &Task{ID: 1, Owner: "acme", Repo: "widget", IssueNumber: 7, PRNumber: 42},
		Forge:        f,
		Log:          slog.New(slog.DiscardHandler),
		ReviewReport: report,
	}
}
