package workflow

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

var errBoom = errors.New("boom")

// fakeDecisionStage is a minimal decision-stage substitute: it runs fn, so a
// test can seed tc.prReview / tc.Outcome / return an error without scripting
// a full nine-phase pipeline's agent calls.
func fakeDecisionStage(name string, fn func(tc *TaskContext) error) Stage {
	return Stage{Name: name, Run: func(_ context.Context, tc *TaskContext) error { return fn(tc) }}
}

func ownPRTaskContext(t *testing.T) (*TaskContext, *fakeForge) {
	t.Helper()
	forge := &fakeForge{}
	tc := &TaskContext{
		Task:  &Task{ID: 1, Owner: "acme", Repo: "widgets", Title: "add a thing", Body: "does the thing"},
		Repo:  configRepoWithBase(t),
		Forge: forge,
		Trees: &fakeTrees{diff: smallDiff},
		Store: &threadSafeStore{},
		Log:   slog.New(slog.DiscardHandler),
	}
	return tc, forge
}

func TestStagePRReviewAndOpenPRParksOnUnchallengedBlockingFinding(t *testing.T) {
	tc, forge := ownPRTaskContext(t)
	seed := fakeDecisionStage("seed", func(tc *TaskContext) error {
		tc.prReview = &prReviewState{scored: []prreview.ScoredFinding{
			{Title: "sql injection", File: "a.go", LineStart: 3, Blocking: true},
		}}
		return nil
	})

	stage := stagePRReviewAndOpenPR([]Stage{seed}, func(*TaskContext) string { return "body" })
	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if tc.Outcome.Status != StatusParked {
		t.Fatalf("outcome = %q, want %q", tc.Outcome.Status, StatusParked)
	}
	if len(forge.calls) != 0 {
		t.Errorf("forge calls = %v, want none: an unchallenged blocking finding must not open a PR", forge.calls)
	}
}

func TestStagePRReviewAndOpenPRProceedsWhenBlockingFindingWasChallenged(t *testing.T) {
	tc, forge := ownPRTaskContext(t)
	seed := fakeDecisionStage("seed", func(tc *TaskContext) error {
		tc.prReview = &prReviewState{scored: []prreview.ScoredFinding{
			{Title: "false alarm", File: "a.go", LineStart: 3, Blocking: true, Adversary: prreview.AdversaryChallenged},
		}}
		return nil
	})

	stage := stagePRReviewAndOpenPR([]Stage{seed}, func(*TaskContext) string { return "body" })
	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if tc.Outcome.Status != StatusPROpen {
		t.Fatalf("outcome = %q, want %q (a challenged blocking finding must not park)", tc.Outcome.Status, StatusPROpen)
	}
	if forge.prNumber == 0 {
		t.Error("no PR was opened")
	}
}

func TestStagePRReviewAndOpenPROpensAndPostsCleanReview(t *testing.T) {
	tc, forge := ownPRTaskContext(t)
	seed := fakeDecisionStage("seed", func(tc *TaskContext) error {
		tc.prReview = &prReviewState{scored: []prreview.ScoredFinding{
			{Title: "nit", File: "a.go", LineStart: 3, Body: "tidy this up"},
		}}
		return nil
	})

	stage := stagePRReviewAndOpenPR([]Stage{seed}, func(*TaskContext) string { return "the pr body" })
	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if tc.Outcome.Status != StatusPROpen {
		t.Fatalf("outcome = %q, want %q", tc.Outcome.Status, StatusPROpen)
	}
	if forge.prNumber != 1 {
		t.Fatalf("PR number = %d, want 1", forge.prNumber)
	}
	if forge.reviewCalls != 1 {
		t.Fatalf("review comment calls = %d, want 1: the advisory finding must post after the PR opens", forge.reviewCalls)
	}
	if forge.reviewNumber != forge.prNumber {
		t.Errorf("review posted against PR %d, want %d", forge.reviewNumber, forge.prNumber)
	}
}

func TestStagePRReviewAndOpenPRHonorsAnEarlyOutcomeFromADecisionStage(t *testing.T) {
	tc, forge := ownPRTaskContext(t)
	seed := fakeDecisionStage("seed", func(tc *TaskContext) error {
		tc.Outcome = Outcome{Status: StatusWaitingHuman, Detail: "awaiting operator review"}
		return nil
	})

	stage := stagePRReviewAndOpenPR([]Stage{seed}, func(*TaskContext) string {
		t.Fatal("PR body should never be built: a decision stage already ended the run")
		return ""
	})
	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if tc.Outcome.Status != StatusWaitingHuman {
		t.Fatalf("outcome = %q, want %q (the decision stage's own outcome must survive)", tc.Outcome.Status, StatusWaitingHuman)
	}
	if len(forge.calls) != 0 {
		t.Errorf("forge calls = %v, want none", forge.calls)
	}
}

func TestStagePRReviewAndOpenPRParksOnADecisionStageFailure(t *testing.T) {
	tc, forge := ownPRTaskContext(t)
	seed := fakeDecisionStage("seed", func(*TaskContext) error {
		return errBoom
	})

	stage := stagePRReviewAndOpenPR([]Stage{seed}, func(*TaskContext) string { return "body" })
	if err := stage.Run(context.Background(), tc); err == nil {
		t.Fatal("want an error: a failed decision stage must fail this stage (the engine parks on a stage error)")
	}
	if len(forge.calls) != 0 {
		t.Errorf("forge calls = %v, want none", forge.calls)
	}
}

func TestLocalPRSourceReadsFromTheTasksOwnWorktree(t *testing.T) {
	tc, _ := ownPRTaskContext(t)
	src := &localPRSource{tc: tc}

	meta, err := src.Metadata(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Title != tc.Task.Title || meta.Body != tc.Task.Body {
		t.Errorf("Metadata = %+v, want the task's own title/body", meta)
	}

	diff, err := src.Diff(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != smallDiff {
		t.Errorf("Diff = %q, want the worktree's own diff", diff)
	}

	headSHA, err := src.Snapshot(context.Background(), "", "", 0, t.TempDir())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if headSHA != "" {
		t.Errorf("headSHA = %q, want empty: fakeTrees does not implement changeStatsReader", headSHA)
	}
}

// configRepoWithBase builds a minimal config.Repo whose BaseBranch()
// resolves without a live git call.
func configRepoWithBase(t *testing.T) config.Repo {
	t.Helper()
	return config.Repo{Base: "main"}
}
