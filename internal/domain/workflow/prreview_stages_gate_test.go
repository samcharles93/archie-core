package workflow

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

func TestStagePRPrecisionGateSkippedWhenConfigOff(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		snapshotDir: t.TempDir(),
		findings:    []prreview.Finding{{Title: "f0", Severity: prreview.SeverityNitpick}},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		t.Fatal("no agent call expected: review.precision_gate is off")
		return agentexec.Result{}, nil
	}}

	if err := stagePRPrecisionGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("precision-gate: %v", err)
	}
	if len(tc.prReview.findings) != 1 {
		t.Fatalf("findings = %d, want 1 (unfiltered: the gate never ran)", len(tc.prReview.findings))
	}
}

func TestStagePRPrecisionGateDropsFindingsMarkedNotWorthPosting(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.PrecisionGate = true
	tc.prReview = &prReviewState{
		snapshotDir: t.TempDir(),
		findings: []prreview.Finding{
			{Title: "concrete", Severity: prreview.SeverityImportant},
			{Title: "nitpick", Severity: prreview.SeverityNitpick},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		if stage == "precision-gate" {
			return captureResult("precision_gate", map[string]any{
				"verdicts": []map[string]any{{"index": 0, "keep": true}, {"index": 1, "keep": false}},
			}), nil
		}
		return passedResult("ok"), nil
	}}

	if err := stagePRPrecisionGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("precision-gate: %v", err)
	}
	if len(tc.prReview.findings) != 1 || tc.prReview.findings[0].Title != "concrete" {
		t.Fatalf("findings = %+v, want only the concrete finding kept", tc.prReview.findings)
	}
}

func TestStagePRPrecisionGateFailedCallKeepsEveryFinding(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.PrecisionGate = true
	tc.prReview = &prReviewState{
		snapshotDir: t.TempDir(),
		findings: []prreview.Finding{
			{Title: "f0", Severity: prreview.SeverityImportant},
			{Title: "f1", Severity: prreview.SeverityNitpick},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return agentexec.Result{Status: agentexec.StatusParked}, nil
	}}

	if err := stagePRPrecisionGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("precision-gate: %v", err)
	}
	if len(tc.prReview.findings) != 2 {
		t.Fatalf("findings = %d, want 2 (recall-first: a failed call filters nothing)", len(tc.prReview.findings))
	}
}

func TestStagePRPrecisionGateRunsNoAgentCallWhenNoFindings(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.PrecisionGate = true
	tc.prReview = &prReviewState{snapshotDir: t.TempDir()}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		t.Fatal("no agent call expected with zero findings")
		return agentexec.Result{}, nil
	}}

	if err := stagePRPrecisionGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("precision-gate: %v", err)
	}
}

func TestStagePROperatorApprovalWaitsWhenConfiguredAndFindingsExist(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.prReview = &prReviewState{
		scored: []prreview.ScoredFinding{{Title: "f0", Score: 0.9}},
	}

	if err := stagePROperatorApproval().Run(context.Background(), tc); err != nil {
		t.Fatalf("operator-approval: %v", err)
	}
	if tc.Outcome.Status != StatusWaitingHuman {
		t.Fatalf("outcome status = %q, want %q", tc.Outcome.Status, StatusWaitingHuman)
	}
}

func TestStagePROperatorApprovalSkipsWhenConfigOff(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		scored: []prreview.ScoredFinding{{Title: "f0", Score: 0.9}},
	}

	if err := stagePROperatorApproval().Run(context.Background(), tc); err != nil {
		t.Fatalf("operator-approval: %v", err)
	}
	if tc.Outcome.Status != "" {
		t.Fatalf("outcome status = %q, want empty (config off, never waits)", tc.Outcome.Status)
	}
}

func TestStagePROperatorApprovalNeverWaitsWithNoFindings(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Cfg.Review.ApproveBeforePost = true
	tc.prReview = &prReviewState{}

	if err := stagePROperatorApproval().Run(context.Background(), tc); err != nil {
		t.Fatalf("operator-approval: %v", err)
	}
	if tc.Outcome.Status != "" {
		t.Fatalf("outcome status = %q, want empty (zero findings never wait, per the PRD)", tc.Outcome.Status)
	}
}
