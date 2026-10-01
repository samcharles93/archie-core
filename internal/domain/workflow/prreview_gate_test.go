package workflow

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// recordingPipelineRunner answers every full-pipeline agent call the way
// scriptedPRReviewPipeline does, and additionally records each call's mission
// by stage name so the instruction-threading tests can read what phase 3 and
// phase 4 were actually told.
type recordingPipelineRunner struct {
	inner *concurrentAgentRunner

	mu       sync.Mutex
	missions map[string]string
}

func newRecordingPipelineRunner(byStage func(stage string) (agentexec.Result, error)) *recordingPipelineRunner {
	return &recordingPipelineRunner{
		inner:    &concurrentAgentRunner{byStage: byStage},
		missions: map[string]string{},
	}
}

func (r *recordingPipelineRunner) Run(
	ctx context.Context, dir string, req agentexec.Request, reporter agentexec.ToolCallReporter,
) (agentexec.Result, error) {
	r.mu.Lock()
	r.missions[req.Stage] = req.Mission
	r.mu.Unlock()
	return r.inner.Run(ctx, dir, req, reporter)
}

func (r *recordingPipelineRunner) mission(stage string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.missions[stage]
}

// offerDocuments renders scored findings the way the gate stage does: the
// stable key an operator's selection names, beside the finding's own encoding.
func offerDocuments(findings ...prreview.ScoredFinding) []workflowtask.ReviewGateFinding {
	out := make([]workflowtask.ReviewGateFinding, len(findings))
	for i, f := range findings {
		out[i] = prreview.GateFinding(f)
	}
	return out
}

// gateReviewFindings is a two-finding review the operator is offered: one to
// keep, one to drop, with distinct bodies so a posted comment proves which
// document won.
func gateReviewFindings() []prreview.ScoredFinding {
	return []prreview.ScoredFinding{
		{Title: "keep me", File: "main.go", LineStart: 1, Body: "the selected finding", Severity: prreview.SeverityImportant},
		{Title: "drop me", File: "main.go", LineStart: 9, Body: "the unselected finding", Severity: prreview.SeverityNitpick},
	}
}

// TestStagePROperatorApprovalWritesTheReviewOffer pins the recorded offer
// (docs/prds/pr-review-operator-response.md, "The review the operator
// answers"): the gate must persist the scored findings, the head SHA, the
// pull request's identity and the workflow the wait resumes before it ends
// the run in waiting_human, because the run that computed the findings cannot
// be resumed partway.
func TestStagePROperatorApprovalWritesTheReviewOffer(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber = "acme", "widgets", 42
	findings := gateReviewFindings()
	tc.prReview = &prReviewState{scored: findings, headSHA: "abc123"}

	if err := stagePROperatorApproval().Run(context.Background(), tc); err != nil {
		t.Fatalf("operator-approval: %v", err)
	}
	if tc.Outcome.Status != StatusWaitingHuman {
		t.Fatalf("outcome status = %q, want %q", tc.Outcome.Status, StatusWaitingHuman)
	}
	gate, ok := workflowtask.DecodeReviewGate(tc.Task.ReviewGate)
	if !ok {
		t.Fatalf("review_gate = %q, want the encoded offer", tc.Task.ReviewGate)
	}
	offered := prreview.PostedFindings(gate.Findings)
	if len(offered) != len(findings) || offered[0].Title != findings[0].Title || offered[0].Body != findings[0].Body {
		t.Fatalf("offer findings = %+v, want the scored review the operator was shown", offered)
	}
	if gate.Findings[0].Key != findings[0].Key() {
		t.Fatalf("offer key = %q, want the finding's stable key %q", gate.Findings[0].Key, findings[0].Key())
	}
	if gate.HeadSHA != "abc123" || gate.Owner != "acme" || gate.Repo != "widgets" || gate.PRNumber != 42 || gate.Workflow != tc.Task.Workflow {
		t.Fatalf("offer provenance = %+v, want the head, PR identity and resuming workflow recorded", gate)
	}
	if gate.Outcome != "" || gate.Instructions != "" {
		t.Fatalf("offer already carries an answer: %+v", gate)
	}
}

// TestStagePROperatorApprovalDoesNotWaitAgainOnAnApprovedGate is the other
// half of the resume: once the operator approved, the re-run must continue to
// the merge gate and output (which posts the recorded review) rather than
// waiting a second time and overwriting the answer.
func TestStagePROperatorApprovalDoesNotWaitAgainOnAnApprovedGate(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.prReview = &prReviewState{scored: gateReviewFindings()}
	tc.Task.ReviewGate = workflowtask.EncodeReviewGate(workflowtask.ReviewGate{
		Findings: offerDocuments(gateReviewFindings()...), Outcome: workflowtask.GateApprove,
	})
	// Intake is the one restore point; this stage test calls it directly
	// rather than scripting the whole pipeline.
	restoreReviewGate(tc)

	if err := stagePROperatorApproval().Run(context.Background(), tc); err != nil {
		t.Fatalf("operator-approval: %v", err)
	}
	if tc.Outcome.Status != "" {
		t.Fatalf("outcome = %q, want empty: an answered gate must not park the run again", tc.Outcome.Status)
	}
}

// TestRereviewInstructionsReachLensAndReviewerMissions pins Decision 3: the
// restored instructions ride a labelled block in runLens's and runReviewer's
// missions only, and a first run -- no gate document, no instructions -- does
// not carry the block at all.
func TestRereviewInstructionsReachLensAndReviewerMissions(t *testing.T) {
	const instructions = "concentrate on the SQL path and the migration ordering"

	run := func(t *testing.T, gate string) *recordingPipelineRunner {
		t.Helper()
		tc := baseTaskContext(t)
		tc.PRSource = &fakePRSource{diff: smallDiff, files: map[string]string{"main.go": "package main\n"}}
		tc.Task.ReviewGate = gate
		runner := newRecordingPipelineRunner(func(stage string) (agentexec.Result, error) {
			switch {
			case stage == "intake-ai-score":
				return captureResult("score_ai_generated", map[string]any{"confidence": 0.1}), nil
			case strings.HasPrefix(stage, "lens-"):
				return captureResult("propose_dimensions", map[string]any{"dimensions": []map[string]any{{
					"name": "defect", "prompt": "review main.go", "target_files": []string{"main.go"}, "priority": 1.0,
				}}}), nil
			case strings.HasPrefix(stage, "reviewer-"):
				return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
			case stage == "consistency":
				return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
			case stage == "adversary":
				return passedResult("no findings to challenge"), nil
			default:
				return passedResult("phase complete"), nil
			}
		})
		tc.Agent = runner
		Run(context.Background(), PRReview(), tc)
		return runner
	}

	answered := workflowtask.EncodeReviewGate(workflowtask.ReviewGate{
		Outcome: workflowtask.GateRereview, Instructions: instructions,
	})
	reReview := run(t, answered)
	for _, stage := range []string{"lens-behaviour", "lens-mechanics", "lens-fit", "reviewer-defect"} {
		if mission := reReview.mission(stage); !strings.Contains(mission, instructions) {
			t.Errorf("%s mission does not carry the operator's instructions:\n%s", stage, mission)
		}
	}
	// Only phases 3 and 4 read them: no other phase's mission may carry them.
	for _, stage := range []string{"intake-ai-score", "anatomy", "merge-gate"} {
		if mission := reReview.mission(stage); strings.Contains(mission, instructions) {
			t.Errorf("%s mission carries the re-review instructions; only phases 3 and 4 may", stage)
		}
	}

	firstRun := run(t, "")
	for _, stage := range []string{"lens-behaviour", "reviewer-defect"} {
		if mission := firstRun.mission(stage); strings.Contains(mission, "Operator instructions") {
			t.Errorf("first-run %s mission carries an operator-instructions block:\n%s", stage, mission)
		}
	}
}

// TestApproveResumePostsTheRecordedSelectionNotTheRecomputedFindings pins the
// PRD's first Verification bullet: an approve posts exactly the findings the
// operator selected, and no finding the resumed phases produced. The resumed
// pipeline recomputes its own (different) finding; only the recorded selection
// may reach the forge.
func TestApproveResumePostsTheRecordedSelectionNotTheRecomputedFindings(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.PRSource = &fakePRSource{diff: smallDiff, files: map[string]string{"main.go": "package main\n"}}
	forge := &fakeForger{}
	tc.Forge = forge

	recorded := gateReviewFindings()
	tc.Task.ReviewGate = workflowtask.EncodeReviewGate(workflowtask.ReviewGate{
		Findings: offerDocuments(recorded...), HeadSHA: "recorded-sha", Owner: "acme", Repo: "widgets", PRNumber: 42,
		Workflow: "pr-review", Outcome: workflowtask.GateApprove, Selection: []string{recorded[0].Key()},
	})

	tc.Agent = newRecordingPipelineRunner(func(stage string) (agentexec.Result, error) {
		switch {
		case stage == "intake-ai-score":
			return captureResult("score_ai_generated", map[string]any{"confidence": 0.1}), nil
		case strings.HasPrefix(stage, "lens-"):
			return captureResult("propose_dimensions", map[string]any{"dimensions": []map[string]any{{
				"name": "defect", "prompt": "review main.go", "target_files": []string{"main.go"}, "priority": 1.0,
			}}}), nil
		case strings.HasPrefix(stage, "reviewer-"):
			return captureResult("report_findings", map[string]any{"findings": []map[string]any{{
				"file": "main.go", "line_start": 3, "severity": "important", "title": "recomputed defect",
				"body": "a finding the resumed phases produced", "evidence": "package main", "confidence": 0.9,
			}}}), nil
		case stage == "evidence-verifier":
			return captureResult("verify_findings", map[string]any{"verdicts": []map[string]any{{"index": 0, "supported": true}}}), nil
		case stage == "adversary":
			return captureResult("adversary_verdicts", map[string]any{"verdicts": []map[string]any{{"index": 0, "verdict": "confirmed"}}}), nil
		case stage == "consistency":
			return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
		case stage == "merge-gate":
			return captureResult("classify_blocking", map[string]any{"blocking": false}), nil
		default:
			return passedResult("phase complete"), nil
		}
	})

	Run(context.Background(), PRReview(), tc)

	if tc.Outcome.Status != StatusCompleted {
		t.Fatalf("outcome = %q, want %q: the approved review posts and the run completes", tc.Outcome.Status, StatusCompleted)
	}
	if len(forge.comments) != 1 {
		t.Fatalf("posted comments = %+v, want exactly the one selected finding", forge.comments)
	}
	if body := forge.comments[0].Body; body != recorded[0].Body {
		t.Fatalf("posted body = %q, want the recorded finding %q, not a recomputed one", body, recorded[0].Body)
	}
	for _, comment := range forge.comments {
		if strings.Contains(comment.Body, "recomputed") {
			t.Fatalf("posted comment %q is a finding the resumed phases produced", comment.Body)
		}
	}
	if forge.reviewedHeadSHA != "recorded-sha" {
		t.Fatalf("reviewed head = %q, want the recorded head SHA", forge.reviewedHeadSHA)
	}
}

// TestApproveResumeWithNoSelectionPostsEveryRecordedFinding is the PRD's
// "an absent selection means all of them": a chat /approve carries no
// selection syntax and must post the whole recorded review.
func TestApproveResumeWithNoSelectionPostsEveryRecordedFinding(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.PRSource = &fakePRSource{diff: smallDiff, files: map[string]string{"main.go": "package main\n"}}
	forge := &fakeForger{}
	tc.Forge = forge

	recorded := gateReviewFindings()
	tc.Task.ReviewGate = workflowtask.EncodeReviewGate(workflowtask.ReviewGate{
		Findings: offerDocuments(recorded...), HeadSHA: "recorded-sha", Owner: "acme", Repo: "widgets", PRNumber: 42,
		Outcome: workflowtask.GateApprove,
	})
	tc.Agent = newRecordingPipelineRunner(func(stage string) (agentexec.Result, error) {
		switch {
		case stage == "intake-ai-score":
			return captureResult("score_ai_generated", map[string]any{"confidence": 0.1}), nil
		case strings.HasPrefix(stage, "lens-"):
			return captureResult("propose_dimensions", map[string]any{"dimensions": []map[string]any{{
				"name": "defect", "prompt": "review main.go", "target_files": []string{"main.go"}, "priority": 1.0,
			}}}), nil
		case strings.HasPrefix(stage, "reviewer-"):
			return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
		case stage == "consistency":
			return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
		default:
			return passedResult("phase complete"), nil
		}
	})

	Run(context.Background(), PRReview(), tc)

	if len(forge.comments) != len(recorded) {
		t.Fatalf("posted comments = %d, want all %d recorded findings", len(forge.comments), len(recorded))
	}
	bodies := make([]string, len(forge.comments))
	for i, c := range forge.comments {
		bodies[i] = c.Body
	}
	if !slices.Contains(bodies, recorded[0].Body) || !slices.Contains(bodies, recorded[1].Body) {
		t.Fatalf("posted bodies = %v, want both recorded findings", bodies)
	}
}

// TestReviewGateDocumentRoundTrip pins the encoding contract itself: the
// document is owned by this package, the store and the wire never parse it,
// and an empty or malformed value must not read as an offer.
func TestReviewGateDocumentRoundTrip(t *testing.T) {
	gate := workflowtask.ReviewGate{
		Findings: offerDocuments(gateReviewFindings()...), HeadSHA: "sha", Owner: "acme", Repo: "widgets",
		PRNumber: 3, Workflow: "pr-review", Outcome: workflowtask.GateApprove,
		Selection: []string{"main.go:1:keep me"}, Instructions: "x",
	}
	decoded, ok := workflowtask.DecodeReviewGate(workflowtask.EncodeReviewGate(gate))
	if !ok {
		t.Fatalf("round trip failed: %q", workflowtask.EncodeReviewGate(gate))
	}
	if !decoded.Approved() || len(decoded.Findings) != 2 || decoded.Selection[0] != gate.Selection[0] {
		t.Fatalf("decoded = %+v, want %+v", decoded, gate)
	}
	if _, ok := workflowtask.DecodeReviewGate(""); ok {
		t.Fatal("an empty column decoded as a document")
	}
	if _, ok := workflowtask.DecodeReviewGate("not json"); ok {
		t.Fatal("a malformed column decoded as a document")
	}
	if _, err := json.Marshal(gate); err != nil {
		t.Fatalf("the document must be JSON-safe: %v", err)
	}
}
