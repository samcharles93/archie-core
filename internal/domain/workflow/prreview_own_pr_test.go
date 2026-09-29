package workflow

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
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

// scriptedPRReviewPipeline answers every agent call the full decision
// pipeline makes with the minimum its decoder accepts, ending with one
// advisory (non-blocking) finding: enough for the pipeline to reach the point
// of opening a PR, and nothing that parks on its own.
func scriptedPRReviewPipeline() *concurrentAgentRunner {
	return &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		switch {
		case stage == "intake-ai-score":
			return captureResult("score_ai_generated", map[string]any{"confidence": 0.1}), nil
		case strings.HasPrefix(stage, "lens-"):
			return captureResult("propose_dimensions", map[string]any{"dimensions": []map[string]any{{
				"name": "defect", "prompt": "review main.go", "target_files": []string{"main.go"}, "priority": 1.0,
			}}}), nil
		case strings.HasPrefix(stage, "reviewer-"):
			return captureResult("report_findings", map[string]any{"findings": []map[string]any{{
				"file": "main.go", "line_start": 1, "severity": "important", "title": "defect",
				"body": "broken", "evidence": "package main", "confidence": 0.9,
			}}}), nil
		case stage == "evidence-verifier":
			return captureResult("verify_findings", map[string]any{"verdicts": []map[string]any{{"index": 0, "supported": true}}}), nil
		case stage == "adversary":
			return captureResult("adversary_verdicts", map[string]any{"verdicts": []map[string]any{{"index": 0, "verdict": "confirmed"}}}), nil
		case stage == "consistency":
			return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
		case stage == "merge-gate":
			return captureResult("classify_blocking", map[string]any{"blocking": false}), nil
		case stage == "polish":
			return captureResult("polish", map[string]any{"body": "tightened"}), nil
		default:
			return passedResult("phase complete"), nil
		}
	}}
}

// TestStagePRReviewAndOpenPRDoesNotParkOnOperatorApproval is the regression
// test for archie-core-7nst. The own-PR trigger splices the pr-review
// decision stages into the implement workflow before it opens its PR, and
// with review.approve_before_post set the operator-approval gate used to end
// that run in waiting_human with no PR in existence. Approving such a park
// re-runs the whole implement workflow against a worktree that already
// carries the change; a builder that then finds nothing to do makes
// StageCommitPush close the issue, so the run completes and the PR is never
// opened. The gate belongs to the standalone pipeline only.
func TestStagePRReviewAndOpenPRDoesNotParkOnOperatorApproval(t *testing.T) {
	tc, forge := ownPRTaskContext(t)
	tc.Task.Workflow = "implement"
	tc.Cfg.Review.ApproveBeforePost = true
	tc.Cfg.Models = map[string]string{"review": "provider/review", "classification": "provider/classification"}
	tc.Agent = scriptedPRReviewPipeline()

	if err := StagePRReviewAndOpenPR(implementPRBody).Run(context.Background(), tc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if tc.Outcome.Status == StatusWaitingHuman {
		t.Fatalf("outcome = %q: the own-PR splice must not park on the operator-approval gate, there is no PR to post to yet", tc.Outcome.Status)
	}
	if tc.Outcome.Status != StatusPROpen {
		t.Fatalf("outcome = %q, want %q", tc.Outcome.Status, StatusPROpen)
	}
	if forge.prNumber == 0 {
		t.Fatal("no PR was opened")
	}
}

// TestPRReviewWorkflowStillParksOnOperatorApproval is the other half of the
// archie-core-7nst fix: the gate is dropped from the own-PR splice, not from
// the pipeline. The standalone pr-review workflow still waits for the
// operator before it posts and before its merge gate runs.
func TestPRReviewWorkflowStillParksOnOperatorApproval(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.PRSource = &fakePRSource{diff: smallDiff, files: map[string]string{"main.go": "package main\n"}}
	tc.Agent = scriptedPRReviewPipeline()
	forge := &fakeForge{}
	tc.Forge = forge

	Run(context.Background(), PRReview(), tc)

	if tc.Outcome.Status != StatusWaitingHuman {
		t.Fatalf("outcome = %q, want %q: the standalone pipeline must still wait for the operator", tc.Outcome.Status, StatusWaitingHuman)
	}
	if len(forge.calls) != 0 || forge.reviewCalls != 0 {
		t.Errorf("forge calls = %v (reviews %d), want none: nothing posts before the operator responds", forge.calls, forge.reviewCalls)
	}
}

// configRepoWithBase builds a minimal config.Repo whose BaseBranch()
// resolves without a live git call.
func configRepoWithBase(t *testing.T) config.Repo {
	t.Helper()
	return config.Repo{Base: "main"}
}
