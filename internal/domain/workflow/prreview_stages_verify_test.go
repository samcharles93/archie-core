package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// writeSnapshotFile is a small helper for tests that need a real file on disk
// for evidence extraction to read.
func writeSnapshotFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStagePRVerificationDropsUnsupportedHighPriorityFindings(t *testing.T) {
	tc := baseTaskContext(t)
	dir := t.TempDir()
	writeSnapshotFile(t, dir, "a.go", "package a\n\nfunc F() int { return 1 }\n")
	tc.prReview = &prReviewState{
		snapshotDir: dir,
		findings: []prreview.Finding{
			{File: "a.go", LineStart: 3, LineEnd: 3, Severity: prreview.SeverityCritical, Title: "unsupported", Confidence: 0.9},
			{File: "a.go", LineStart: 3, LineEnd: 3, Severity: prreview.SeverityNitpick, Title: "low priority", Confidence: 0.5},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		switch stage {
		case "evidence-verifier":
			return captureResult("verify_findings", map[string]any{
				"verdicts": []map[string]any{{"index": 0, "supported": false}},
			}), nil
		case "adversary":
			return captureResult("adversary_verdicts", map[string]any{"verdicts": []map[string]any{}}), nil
		default:
			return passedResult("ok"), nil
		}
	}}

	if err := stagePRVerification().Run(context.Background(), tc); err != nil {
		t.Fatalf("verification: %v", err)
	}
	if len(tc.prReview.findings) != 1 {
		t.Fatalf("findings = %d, want 1 (the unsupported critical finding dropped)", len(tc.prReview.findings))
	}
	if tc.prReview.findings[0].Title != "low priority" {
		t.Errorf("surviving finding = %q, want the low-priority one the verifier never checked", tc.prReview.findings[0].Title)
	}
}

func TestStagePRVerificationAppliesAdversaryVerdicts(t *testing.T) {
	tc := baseTaskContext(t)
	dir := t.TempDir()
	writeSnapshotFile(t, dir, "a.go", "package a\n\nfunc F() int { return 1 }\n")
	tc.prReview = &prReviewState{
		snapshotDir: dir,
		findings: []prreview.Finding{
			{File: "a.go", LineStart: 3, LineEnd: 3, Severity: prreview.SeverityImportant, Title: "f0", Confidence: 0.9},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		switch stage {
		case "evidence-verifier":
			return captureResult("verify_findings", map[string]any{
				"verdicts": []map[string]any{{"index": 0, "supported": true}},
			}), nil
		case "adversary":
			return captureResult("adversary_verdicts", map[string]any{
				"verdicts": []map[string]any{{"index": 0, "verdict": "confirmed"}},
			}), nil
		default:
			return passedResult("ok"), nil
		}
	}}

	if err := stagePRVerification().Run(context.Background(), tc); err != nil {
		t.Fatalf("verification: %v", err)
	}
	if len(tc.prReview.findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(tc.prReview.findings))
	}
	if tc.prReview.findings[0].Adversary != prreview.AdversaryConfirmed {
		t.Errorf("adversary verdict = %q, want confirmed", tc.prReview.findings[0].Adversary)
	}
}

func TestStagePRVerificationRunsNoAgentCallsWhenNoFindings(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{snapshotDir: t.TempDir()}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		t.Fatal("no agent call expected with zero findings")
		return agentexec.Result{}, nil
	}}
	if err := stagePRVerification().Run(context.Background(), tc); err != nil {
		t.Fatalf("verification: %v", err)
	}
}

func TestStagePRCompoundMarksFlaggedFindingsWithinCluster(t *testing.T) {
	tc := baseTaskContext(t)
	dir := t.TempDir()
	tc.prReview = &prReviewState{
		snapshotDir: dir,
		clusters:    []prreview.Cluster{{ID: "cluster_0", Files: []string{"a.go", "b.go"}}},
		findings: []prreview.Finding{
			{File: "a.go", LineStart: 1, Title: "f0", Severity: prreview.SeverityImportant, Confidence: 0.9},
			{File: "b.go", LineStart: 1, Title: "f1", Severity: prreview.SeverityImportant, Confidence: 0.9},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		if stage == "compound-cluster_0" {
			return captureResult("compound_findings", map[string]any{"indices": []int{0, 1}}), nil
		}
		return passedResult("ok"), nil
	}}

	findings, err := runCompoundCheck(context.Background(), tc, tc.prReview.findings)
	if err != nil {
		t.Fatalf("runCompoundCheck: %v", err)
	}
	for _, f := range findings {
		if !f.Compound {
			t.Errorf("finding %q Compound = false, want true (both findings in the flagged cluster)", f.Title)
		}
	}
}

func TestStagePRCoverageConsistencyRunsGapReviewersAndConsistencyInParallel(t *testing.T) {
	tc := baseTaskContext(t)
	dir := t.TempDir()
	tc.prReview = &prReviewState{
		snapshotDir:  dir,
		clusters:     []prreview.Cluster{{ID: "cluster_0", Name: "pkg", Files: []string{"pkg/a.go"}}},
		highExposure: []string{"pkg/shared.go"},
		findings:     nil,
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		switch stage {
		case "consistency":
			return captureResult("report_findings", map[string]any{
				"findings": []map[string]any{
					{"file": "pkg/a.go", "line_start": 1, "severity": "important", "title": "broken obligation", "body": "b", "evidence": "e", "confidence": 0.8},
				},
			}), nil
		default:
			// Gap reviewers reuse runReviewer's report_findings shape.
			return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
		}
	}}

	if err := stagePRCoverageConsistency().Run(context.Background(), tc); err != nil {
		t.Fatalf("coverage-consistency: %v", err)
	}

	foundConsistency := false
	for _, f := range tc.prReview.findings {
		if f.Title == "broken obligation" {
			foundConsistency = true
		}
	}
	if !foundConsistency {
		t.Error("consistency verification's finding was not appended")
	}
}

func TestCoverageGapCapsAtTwoRounds(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		snapshotDir: t.TempDir(),
		clusters:    []prreview.Cluster{{ID: "cluster_0", Files: []string{"a.go"}}},
	}
	var calls int
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		calls++
		// Every gap reviewer call reports nothing, so the cluster stays
		// uncovered and a naive loop would run forever without the cap.
		return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
	}}

	runCoverageGate(context.Background(), tc)

	if calls != 2 {
		t.Errorf("gap reviewer calls = %d, want exactly 2 (the round cap)", calls)
	}
}

func TestStagePRMergeGateSetsBlockingIndependentlyPerFinding(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		snapshotDir: t.TempDir(),
		scored: []prreview.ScoredFinding{
			{File: "a.go", LineStart: 1, Title: "sql injection"},
			{File: "b.go", LineStart: 2, Title: "unused variable"},
		},
	}
	// One call per finding: alternate the verdict by call order, so a bug
	// that broadcasts one verdict to every finding (rather than one call
	// each) would leave both findings the same instead of split.
	var calls atomic.Int32
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		n := calls.Add(1)
		return captureResult("classify_blocking", map[string]any{"blocking": n == 1}), nil
	}}

	if err := stagePRMergeGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("merge-gate: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("classification calls = %d, want 2 (one per finding)", calls.Load())
	}
	blockingCount := 0
	for _, f := range tc.prReview.scored {
		if f.Blocking {
			blockingCount++
		}
	}
	if blockingCount != 1 {
		t.Errorf("blocking findings = %d, want exactly 1 (one call returned true, the other false)", blockingCount)
	}
}

func TestStagePRMergeGateFailedCallLeavesFindingAdvisory(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		snapshotDir: t.TempDir(),
		scored:      []prreview.ScoredFinding{{File: "a.go", LineStart: 1, Title: "t", Blocking: true}},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return agentexec.Result{}, errors.New("model unavailable")
	}}

	if err := stagePRMergeGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("merge-gate: %v", err)
	}
	if tc.prReview.scored[0].Blocking {
		t.Error("Blocking = true, want false: a failed merge-gate call must leave the finding advisory, per the PRD")
	}
}

func TestStagePRMergeGateSkipsEntirelyWhenNoFindings(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{snapshotDir: t.TempDir()}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		t.Fatal("no agent call should run when there are no findings to classify")
		return agentexec.Result{}, nil
	}}

	if err := stagePRMergeGate().Run(context.Background(), tc); err != nil {
		t.Fatalf("merge-gate: %v", err)
	}
}
