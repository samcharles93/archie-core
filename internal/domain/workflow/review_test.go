package workflow

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// fakeReviewer records the request it was given and returns a canned
// report, standing in for the app-layer agent.Subagent-backed Reviewer.
//
// It reads what it needs from req.SnapshotDir synchronously, inside
// Review, because the stage cleans the snapshot up as soon as Review
// returns -- a real Reviewer implementation faces the same constraint.
type fakeReviewer struct {
	gotRequest      ReviewRequest
	snapshotContent string
	snapshotHadGit  bool
	called          bool
	report          ReviewReport
}

func (f *fakeReviewer) Review(_ context.Context, req ReviewRequest) ReviewReport {
	f.called = true
	f.gotRequest = req
	if content, err := os.ReadFile(filepath.Join(req.SnapshotDir, "feature.go")); err == nil {
		f.snapshotContent = string(content)
	}
	if _, err := os.Stat(filepath.Join(req.SnapshotDir, ".git")); err == nil {
		f.snapshotHadGit = true
	}
	return f.report
}

// reviewTaskContext builds a TaskContext over a real git repo with a
// tracked, committed change against base, so StageReview's Diff/Snapshot
// calls have real content to work with.
func reviewTaskContext(t *testing.T, enabled bool) (*TaskContext, string) {
	t.Helper()
	dir := gitRepoWithOriginRef(t, "main")
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "feat: add feature.go")

	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Owner: "acme", Repo: "todo", IssueNumber: 42, Title: "Add feature", Body: "Please add it."},
		Repo:  config.Repo{Owner: "acme", Name: "todo", Base: "main", ReviewEnabled: enabled},
		Cfg:   config.Config{},
		Trees: &worktree.Manager{WorkDir: t.TempDir()},
		Dir:   dir,
		Log:   slog.New(slog.DiscardHandler),
	}
	return tc, dir
}

func TestStageReviewIsInertUnlessEnabled(t *testing.T) {
	tc, _ := reviewTaskContext(t, false)
	reviewer := &fakeReviewer{}
	tc.Reviewer = reviewer

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if reviewer.called {
		t.Error("StageReview called the reviewer despite Repo.ReviewEnabled == false")
	}
	if tc.Outcome.Status != "" {
		t.Errorf("Outcome.Status = %q, want unset", tc.Outcome.Status)
	}
}

func TestStageReviewParksWhenEnabledWithNoReviewerWired(t *testing.T) {
	tc, _ := reviewTaskContext(t, true)
	tc.Reviewer = nil

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if tc.Outcome.Status != store.StatusParked {
		t.Errorf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusParked)
	}
}

func TestStageReviewParksFailClosedWhenReviewDidNotRun(t *testing.T) {
	tc, _ := reviewTaskContext(t, true)
	reviewer := &fakeReviewer{report: NewNotRunReviewReport("provider outage")}
	tc.Reviewer = reviewer

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if !reviewer.called {
		t.Fatal("reviewer was not called")
	}
	if tc.Outcome.Status != store.StatusParked {
		t.Errorf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusParked)
	}
	if got := tc.Outcome.Detail; !containsAll(got, "did not run", "provider outage") {
		t.Errorf("Outcome.Detail = %q, want it to say the review did not run and why", got)
	}
}

func TestStageReviewParksOnConfirmedErrorFinding(t *testing.T) {
	tc, _ := reviewTaskContext(t, true)
	blocking := ReviewFinding{
		File: "feature.go", Line: 1, Defect: "nil deref",
		FailureScenario: "calling Foo(nil) panics",
		Verdict:         ReviewVerdictConfirmed, Level: ReviewLevelError, Category: ReviewCategoryNilRisk,
	}
	reviewer := &fakeReviewer{report: NewCompletedReviewReport([]ReviewFinding{blocking}, "found one defect")}
	tc.Reviewer = reviewer

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if tc.Outcome.Status != store.StatusParked {
		t.Errorf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusParked)
	}
	if got := tc.Outcome.Detail; !containsAll(got, "feature.go", "nil deref") {
		t.Errorf("Outcome.Detail = %q, want the blocking finding rendered", got)
	}
}

func TestStageReviewDoesNotBlockOnWarnOrPlausibleFindings(t *testing.T) {
	tc, _ := reviewTaskContext(t, true)
	findings := []ReviewFinding{
		{
			File: "feature.go", Line: 1, Defect: "could be tidier",
			FailureScenario: "n/a", Verdict: ReviewVerdictConfirmed, Level: ReviewLevelWarn,
			Category: ReviewCategoryOther,
		},
		{
			File: "feature.go", Line: 2, Defect: "maybe a race",
			FailureScenario: "under heavy concurrency", Verdict: ReviewVerdictPlausible, Level: ReviewLevelError,
			Category: ReviewCategoryRace,
		},
	}
	reviewer := &fakeReviewer{report: NewCompletedReviewReport(findings, "found two non-blocking findings")}
	tc.Reviewer = reviewer

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if tc.Outcome.Status != "" {
		t.Errorf("Outcome.Status = %q, want unset (non-blocking findings must not park)", tc.Outcome.Status)
	}
}

func TestStageReviewGivesReviewerAnIsolatedSnapshotDiffAndIssueText(t *testing.T) {
	tc, dir := reviewTaskContext(t, true)
	reviewer := &fakeReviewer{report: NewCompletedReviewReport(nil, "clean")}
	tc.Reviewer = reviewer

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if !reviewer.called {
		t.Fatal("reviewer was not called")
	}
	req := reviewer.gotRequest

	if req.SnapshotDir == "" {
		t.Fatal("ReviewRequest.SnapshotDir is empty")
	}
	if req.SnapshotDir == dir {
		t.Errorf("ReviewRequest.SnapshotDir = %q reuses the task worktree; want a distinct snapshot", req.SnapshotDir)
	}
	if reviewer.snapshotContent != "package main\n" {
		t.Errorf("snapshotted feature.go = %q, want %q", reviewer.snapshotContent, "package main\n")
	}
	if reviewer.snapshotHadGit {
		t.Error("ReviewRequest.SnapshotDir contains .git, want it stripped")
	}
	if req.Diff == "" {
		t.Error("ReviewRequest.Diff is empty, want the feature.go diff")
	}
	if !containsAll(req.IssueText, "Add feature", "Please add it.") {
		t.Errorf("ReviewRequest.IssueText = %q, want it to contain the task title and body", req.IssueText)
	}

	// The snapshot is scratch, cleaned up once the stage returns -- it
	// must not leak into the task worktree's directory tree permanently.
	if _, err := os.Stat(req.SnapshotDir); !os.IsNotExist(err) {
		t.Errorf("ReviewRequest.SnapshotDir %q still exists after the stage returned, want it cleaned up", req.SnapshotDir)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
