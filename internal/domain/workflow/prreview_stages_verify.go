package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// stagePRVerification is pipeline phase 5, over the review findings phase 4
// produced: extract each finding's evidence package (code), check the
// high-priority findings against theirs (an evidence verifier agent), have
// an adversary confirm or challenge every finding and add anything every
// reviewer missed, then flag within-cluster compound defects
// (docs/prds/pr-review-agent.md, phase 5).
func stagePRVerification() Stage {
	return Stage{Name: "verification", Run: func(ctx context.Context, tc *TaskContext) error {
		if len(tc.prReview.findings) == 0 {
			return nil
		}

		verified, err := runEvidenceVerifier(ctx, tc, tc.prReview.findings)
		if err != nil {
			return fmt.Errorf("evidence verifier: %w", err)
		}

		confirmed, err := runAdversary(ctx, tc, verified)
		if err != nil {
			return fmt.Errorf("adversary: %w", err)
		}

		compounded, err := runCompoundCheck(ctx, tc, confirmed)
		if err != nil {
			return fmt.Errorf("compound check: %w", err)
		}

		tc.prReview.findings = compounded
		return nil
	}}
}

// evidencePackageFor extracts a finding's evidence package from the
// snapshot, reusing afbk.1's ExtractEvidence. A file the snapshot no longer
// has (a finding pointing at a path that does not exist) extracts an empty
// package rather than failing the stage: that emptiness is itself the
// evidence verifier's answer.
func evidencePackageFor(tc *TaskContext, f prreview.Finding) prreview.EvidencePackage {
	pkg, err := prreview.ExtractEvidence(os.DirFS(tc.prReview.snapshotDir), prreview.EvidenceRequest{
		File: f.File, LineStart: f.LineStart, LineEnd: f.LineEnd,
		Title: f.Title, Body: f.Body, Evidence: f.Evidence,
	})
	if err != nil {
		return prreview.EvidencePackage{FindingTitle: f.Title, File: f.File}
	}
	return pkg
}

// verifyFindingsSchema is the evidence verifier's capture-tool schema.
var verifyFindingsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"verdicts": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"index": {"type": "integer"},
					"supported": {"type": "boolean"},
					"reason": {"type": "string"}
				},
				"required": ["index", "supported"]
			}
		}
	},
	"required": ["verdicts"]
}`)

// evidenceCandidate is one high-priority finding the evidence verifier is
// asked to check, paired with its index in the caller's original slice: the
// verifier's answer references findings by that index, not by content.
type evidenceCandidate struct {
	index   int
	finding prreview.Finding
}

// highPriorityCandidates picks the findings runEvidenceVerifier checks:
// every high-priority finding, each still carrying its position in findings.
func highPriorityCandidates(findings []prreview.Finding) []evidenceCandidate {
	var candidates []evidenceCandidate
	for i, f := range findings {
		if prreview.IsHighPriority(f) {
			candidates = append(candidates, evidenceCandidate{index: i, finding: f})
		}
	}
	return candidates
}

// evidenceListing renders each candidate's claim and evidence package as the
// evidence verifier's mission text.
func evidenceListing(tc *TaskContext, candidates []evidenceCandidate) string {
	var listing strings.Builder
	for _, c := range candidates {
		pkg := evidencePackageFor(tc, c.finding)
		fmt.Fprintf(&listing, "Finding %d: %s (%s:%d)\nClaim: %s\nQuoted evidence: %s\nCode at the cited lines:\n%s\n\n",
			c.index, c.finding.Title, c.finding.File, c.finding.LineStart, c.finding.Body, c.finding.Evidence, pkg.PrimaryCode)
	}
	return listing.String()
}

// runEvidenceVerifier runs phase 5's evidence verifier over every
// high-priority (critical or important) finding, dropping the ones it finds
// unsupported by their own evidence package. A finding below high priority is
// never sent, and always kept: the verifier exists to catch a hallucinated
// critical, not to grade a nitpick. A failed or malformed verifier call keeps
// every finding unfiltered -- the pipeline is recall-first, so a verifier
// that could not run is not evidence any finding is wrong; the error is
// intentionally swallowed here, not propagated.
func runEvidenceVerifier(ctx context.Context, tc *TaskContext, findings []prreview.Finding) ([]prreview.Finding, error) {
	candidates := highPriorityCandidates(findings)
	if len(candidates) == 0 {
		return findings, nil
	}

	mission := fmt.Sprintf(
		"Each finding below cites code as evidence for its claim. Check whether the "+
			"quoted code at the cited lines actually supports the claim -- not whether "+
			"the claim is a good catch, only whether the evidence is real and says what "+
			"the finding says it says.\n\n%s\n"+
			"Call verify_findings exactly once with one verdict per finding index, then "+
			"call finish with status \"passed\".",
		evidenceListing(tc, candidates),
	)
	res, err := runPRReviewAgentRecorded(ctx, tc, tc.prReview.snapshotDir, "evidence-verifier", "review", mission, 15, []agentexec.CaptureTool{{
		Name: "verify_findings", Description: "Record each finding's evidence verdict. Call exactly once, before finish.",
		Parameters: verifyFindingsSchema, RequiredFields: []string{"verdicts"}, MaxCalls: 1,
	}})
	if err != nil || res.Status != agentexec.StatusPassed {
		return findings, nil //nolint:nilerr // recall-first: a verifier that could not run is not evidence any finding is wrong, so nothing is filtered
	}
	calls := res.Captures["verify_findings"]
	if len(calls) != 1 {
		return findings, nil
	}
	var captured struct {
		Verdicts []struct {
			Index     int  `json:"index"`
			Supported bool `json:"supported"`
		} `json:"verdicts"`
	}
	if err := json.Unmarshal(calls[0], &captured); err != nil {
		return findings, nil //nolint:nilerr // malformed verifier output keeps findings unfiltered rather than failing the stage
	}
	unsupported := make(map[int]bool, len(captured.Verdicts))
	for _, v := range captured.Verdicts {
		if !v.Supported {
			unsupported[v.Index] = true
		}
	}

	kept := make([]prreview.Finding, 0, len(findings))
	for i, f := range findings {
		if unsupported[i] {
			continue
		}
		kept = append(kept, f)
	}
	return kept, nil
}

// adversaryVerdictsSchema is the adversary's verdict capture-tool schema.
var adversaryVerdictsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"verdicts": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"index": {"type": "integer"},
					"verdict": {"type": "string", "enum": ["confirmed", "challenged"]},
					"reason": {"type": "string"}
				},
				"required": ["index", "verdict"]
			}
		}
	},
	"required": ["verdicts"]
}`)

// adversarySkepticism is the extra instruction the adversary's mission
// carries when intake scored the PR as likely machine-written, per the PRD.
func adversarySkepticism(aiGenerated float64) string {
	if prreview.IsAIGenerated(aiGenerated) {
		return "This PR's own description reads as likely machine-written: be more sceptical " +
			"than usual, since a machine-written description is more likely to accompany " +
			"machine-written code with the same failure modes."
	}
	return "This PR's own description does not read as machine-written; review normally."
}

// findingsListing renders every finding, numbered by its index, for a
// mission that must reference findings back by that index.
func findingsListing(findings []prreview.Finding) string {
	var listing strings.Builder
	for i, f := range findings {
		fmt.Fprintf(&listing, "Finding %d [%s] %s (%s:%d): %s\nEvidence: %s\n\n",
			i, f.Severity, f.Title, f.File, f.LineStart, f.Body, f.Evidence)
	}
	return listing.String()
}

// applyAdversaryVerdicts decodes one adversary_verdicts capture and sets each
// referenced finding's Adversary field. An out-of-range index (malformed or
// adversarial model output) is skipped rather than panicking.
func applyAdversaryVerdicts(out []prreview.Finding, calls []json.RawMessage) {
	if len(calls) != 1 {
		return
	}
	var captured struct {
		Verdicts []struct {
			Index   int    `json:"index"`
			Verdict string `json:"verdict"`
		} `json:"verdicts"`
	}
	if json.Unmarshal(calls[0], &captured) != nil {
		return
	}
	for _, v := range captured.Verdicts {
		if v.Index < 0 || v.Index >= len(out) {
			continue
		}
		switch v.Verdict {
		case string(prreview.AdversaryConfirmed):
			out[v.Index].Adversary = prreview.AdversaryConfirmed
		case string(prreview.AdversaryChallenged):
			out[v.Index].Adversary = prreview.AdversaryChallenged
		}
	}
}

// runAdversary runs phase 5's adversary over every surviving finding at
// once: it confirms or challenges each by index, and may report new findings
// no reviewer caught. It reads intake's machine-written confidence and is
// told to be more sceptical above the threshold, per the PRD. A failed or
// malformed call leaves every finding's Adversary verdict unset (neither
// confirmed nor challenged) and adds nothing -- an adversary that could not
// run has reached no verdict, which is different from confirming everything.
func runAdversary(ctx context.Context, tc *TaskContext, findings []prreview.Finding) ([]prreview.Finding, error) {
	if len(findings) == 0 {
		return findings, nil
	}

	mission := fmt.Sprintf(
		"You are the adversary reviewing this pull request's findings. For each finding "+
			"below, confirm it (you agree it is a real defect) or challenge it (you believe "+
			"it is wrong or not actually a problem). Read the snapshot yourself to check. "+
			"%s Also look for any defect every reviewer missed and report it as a new "+
			"finding.\n\n%s\n"+
			"Call adversary_verdicts exactly once with one verdict per finding index. If you "+
			"found anything new, call report_findings once too (skip it if you found "+
			"nothing new). Then call finish with status \"passed\".",
		adversarySkepticism(tc.prReview.aiGenerated), findingsListing(findings),
	)
	res, err := runPRReviewAgentRecorded(ctx, tc, tc.prReview.snapshotDir, "adversary", "review", mission, 25, []agentexec.CaptureTool{
		{
			Name: "adversary_verdicts", Description: "Record each finding's adversary verdict. Call exactly once, before finish.",
			Parameters: adversaryVerdictsSchema, RequiredFields: []string{"verdicts"}, MaxCalls: 1,
		},
		reportFindingsTool,
	})
	if err != nil || res.Status != agentexec.StatusPassed {
		return findings, nil //nolint:nilerr // a call that could not run reached no verdict, which the pipeline treats as neutral, not as a stage failure
	}

	out := append([]prreview.Finding{}, findings...)
	applyAdversaryVerdicts(out, res.Captures["adversary_verdicts"])
	if extra, err := decodeReportedFindings(res.Captures["report_findings"], "adversary"); err == nil {
		out = append(out, extra...)
	}
	return out, nil
}

// runCompoundCheck runs phase 5's compound-defect check once per cluster
// that has more than one finding, in parallel, bounded the same as every
// other pr-review fan-out. It flags the findings the agent names as
// compounding (Finding.Compound = true), which is what scoring's compound
// multiplier reads. A cluster with at most one finding cannot compound with
// anything and is skipped without an agent call.
func runCompoundCheck(ctx context.Context, tc *TaskContext, findings []prreview.Finding) ([]prreview.Finding, error) {
	grouped := prreview.ClusterFindings(tc.prReview.clusters, findings)
	var clusterIDs []string
	for id, members := range grouped {
		if len(members) > 1 {
			clusterIDs = append(clusterIDs, id)
		}
	}
	if len(clusterIDs) == 0 {
		return findings, nil
	}

	flagged := make([]map[string]bool, len(clusterIDs))
	forEachBounded(prReviewConcurrency, len(clusterIDs), func(i int) {
		flagged[i] = runCompoundClusterCheck(ctx, tc, clusterIDs[i], grouped[clusterIDs[i]])
	})

	compoundTitles := map[string]bool{}
	for _, set := range flagged {
		for title := range set {
			compoundTitles[title] = true
		}
	}
	out := append([]prreview.Finding{}, findings...)
	for i, f := range out {
		if compoundTitles[compoundKey(f)] {
			out[i].Compound = true
		}
	}
	return out, nil
}

// compoundKey is how runCompoundCheck matches a cluster agent's flagged
// member back to a finding in the full list: findings carry no stable ID, so
// (file, line, title) is the identity a compound call's answer is matched on.
func compoundKey(f prreview.Finding) string {
	return fmt.Sprintf("%s:%d:%s", f.File, f.LineStart, f.Title)
}

// runCompoundClusterCheck runs one phase-5 compound call over one cluster's
// findings, returning the set of member keys (compoundKey) the agent flagged
// as only defective in combination. A failed or malformed call flags nothing.
func runCompoundClusterCheck(ctx context.Context, tc *TaskContext, clusterID string, members []prreview.Finding) map[string]bool {
	var listing strings.Builder
	for i, f := range members {
		fmt.Fprintf(&listing, "Finding %d [%s] %s (%s:%d): %s\n\n", i, f.Severity, f.Title, f.File, f.LineStart, f.Body)
	}
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"indices": {"type": "array", "items": {"type": "integer"}}
		},
		"required": ["indices"]
	}`)
	mission := fmt.Sprintf(
		"These findings all touch the same cluster of files. Read them together: is "+
			"there a defect that only exists because of how two or more of them combine -- "+
			"one that neither finding describes on its own? If so, call compound_findings "+
			"once with the indices of the findings that compound (an empty array if none "+
			"do). Then call finish with status \"passed\".\n\n%s", listing.String(),
	)
	res, err := runPRReviewAgentRecorded(ctx, tc, tc.prReview.snapshotDir, "compound-"+clusterID, "review", mission, 15, []agentexec.CaptureTool{{
		Name: "compound_findings", Description: "Record which findings compound. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"indices"}, MaxCalls: 1,
	}})
	if err != nil || res.Status != agentexec.StatusPassed {
		return nil //nolint:nilerr // a cluster call that could not run flags nothing rather than failing the whole compound-check stage
	}
	calls := res.Captures["compound_findings"]
	if len(calls) != 1 {
		return nil
	}
	var captured struct {
		Indices []int `json:"indices"`
	}
	if err := json.Unmarshal(calls[0], &captured); err != nil {
		return nil
	}
	flagged := make(map[string]bool, len(captured.Indices))
	for _, index := range captured.Indices {
		if index < 0 || index >= len(members) {
			continue
		}
		flagged[compoundKey(members[index])] = true
	}
	return flagged
}

// prReviewGapRounds is phase 6's cap on gap-review rounds: at most 2, per the
// PRD, so a coverage gap the gap reviewers cannot close does not loop forever.
const prReviewGapRounds = 2

// stagePRCoverageConsistency is pipeline phase 6: the coverage gate and
// consistency verification run in parallel and both append to
// tc.prReview.findings, guarded by prReviewState.findingsMu
// (docs/prds/pr-review-agent.md, phase 6: "Coverage and consistency, in
// parallel").
func stagePRCoverageConsistency() Stage {
	return Stage{Name: "coverage-consistency", Run: func(ctx context.Context, tc *TaskContext) error {
		var wg sync.WaitGroup
		var consistencyErr error
		wg.Go(func() {
			consistencyErr = runConsistencyVerification(ctx, tc)
		})
		runCoverageGate(ctx, tc)
		wg.Wait()
		return consistencyErr
	}}
}

// runCoverageGate is phase 6's coverage gate: for up to prReviewGapRounds
// rounds, find the clusters and high-exposure blast-radius files no finding
// covers, run one gap reviewer per gap (reusing runReviewer, the same
// reviewer definition phase 4 uses), and append whatever they find. It stops
// early once nothing is uncovered, so a change that phase 4 already covered
// fully never spends a gap-review call.
func runCoverageGate(ctx context.Context, tc *TaskContext) {
	for range prReviewGapRounds {
		tc.prReview.findingsMu.Lock()
		uncoveredClusters, uncoveredFiles := prreview.CoverageGaps(tc.prReview.clusters, tc.prReview.highExposure, tc.prReview.findings)
		tc.prReview.findingsMu.Unlock()
		if len(uncoveredClusters) == 0 && len(uncoveredFiles) == 0 {
			return
		}

		dims := gapDimensions(uncoveredClusters, uncoveredFiles)
		found := make([][]prreview.Finding, len(dims))
		forEachBounded(prReviewConcurrency, len(dims), func(i int) {
			found[i] = runReviewer(ctx, tc, dims[i])
		})

		tc.prReview.findingsMu.Lock()
		for _, f := range found {
			tc.prReview.findings = append(tc.prReview.findings, f...)
		}
		tc.prReview.findingsMu.Unlock()
	}
}

// gapDimensions builds one reviewer dimension per coverage gap: a cluster's
// files together (a cluster is reviewed as one unit, the way phase 4 would
// have if a lens had proposed it), and one per uncovered high-exposure file.
func gapDimensions(clusters []prreview.Cluster, files []string) []prreview.Dimension {
	dims := make([]prreview.Dimension, 0, len(clusters)+len(files))
	for _, cluster := range clusters {
		dims = append(dims, prreview.Dimension{
			Name:        "gap-" + cluster.ID,
			Prompt:      "This cluster of changed files was not reached by any review dimension. Review it for anything a reviewer would normally catch.",
			TargetFiles: cluster.Files,
		})
	}
	for _, file := range files {
		dims = append(dims, prreview.Dimension{
			Name:        "gap-exposure-" + file,
			Prompt:      "This file is reached by more than one changed file (high blast-radius exposure) but no reviewer covered it. Review it for anything the changes upstream may have broken here.",
			TargetFiles: []string{file},
		})
	}
	return dims
}

// runConsistencyVerification is phase 6's consistency verification: one
// agent call lists the cross-location obligations the changed code creates
// and verifies each by reading both ends, reporting only the broken ones as
// findings (the PRD's own wording -- "a broken obligation becomes a
// finding"). It reuses report_findings, the same shape every other
// findings-reporting call in the pipeline uses.
func runConsistencyVerification(ctx context.Context, tc *TaskContext) error {
	mission := fmt.Sprintf(
		"Read this pull request's diff and the surrounding repository. List every "+
			"cross-location obligation the changed code creates: what must be true "+
			"elsewhere in the codebase for a changed line to be correct (a caller that "+
			"must pass a new argument, a schema a struct must still match, a doc that must "+
			"still describe the behaviour, an invariant a comment states that the new code "+
			"must uphold). For each obligation, verify it by reading both ends yourself. "+
			"Report only the obligations you found broken as findings -- an obligation you "+
			"checked and found intact is not a finding.\n\nDiff:\n%s\n\n"+
			"Call report_findings once (an empty array if every obligation you checked "+
			"held), then call finish with status \"passed\".",
		clip(tc.prReview.diff, 60000),
	)
	res, err := runPRReviewAgentRecorded(ctx, tc, tc.prReview.snapshotDir, "consistency", "review", mission, 25, []agentexec.CaptureTool{reportFindingsTool})
	if err != nil || res.Status != agentexec.StatusPassed {
		return nil //nolint:nilerr // a consistency call that could not run reports no broken obligations rather than failing the stage
	}
	found, err := decodeReportedFindings(res.Captures["report_findings"], "consistency")
	if err != nil {
		return nil //nolint:nilerr // malformed consistency output reports no broken obligations rather than failing the stage
	}

	tc.prReview.findingsMu.Lock()
	tc.prReview.findings = append(tc.prReview.findings, found...)
	tc.prReview.findingsMu.Unlock()
	return nil
}

// mergeGateSchema is the merge gate's classification-call capture-tool
// schema.
var mergeGateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"blocking": {"type": "boolean", "description": "true only for a broken build, a security hole, data loss, a contract break or a regression."}
	},
	"required": ["blocking"]
}`)

// stagePRMergeGate is pipeline phase 8: one classification call per
// surviving finding decides blocking or advisory (docs/prds/pr-review-
// agent.md, phase 8). Blocking is narrow, and a failed or malformed call
// leaves a finding advisory -- the same fallback every other pr-review
// verdict call in this pipeline uses when its agent call could not run.
func stagePRMergeGate() Stage {
	return Stage{Name: "merge-gate", Run: func(ctx context.Context, tc *TaskContext) error {
		if len(tc.prReview.scored) == 0 {
			return nil
		}
		if budgetExhausted(tc, "merge-gate") {
			skipPhase(tc, "merge-gate")
			return nil
		}
		blocking := make([]bool, len(tc.prReview.scored))
		forEachBounded(prReviewConcurrency, len(tc.prReview.scored), func(i int) {
			blocking[i] = runMergeGateCall(ctx, tc, tc.prReview.scored[i])
		})
		for i := range tc.prReview.scored {
			tc.prReview.scored[i].Blocking = blocking[i]
		}
		return nil
	}}
}

// runMergeGateCall runs one phase-8 classification call over one finding.
func runMergeGateCall(ctx context.Context, tc *TaskContext, f prreview.ScoredFinding) bool {
	mission := fmt.Sprintf(
		"Decide whether this finding must block the change from merging. Blocking is "+
			"narrow: only a broken build, a security hole, data loss, a contract break, or "+
			"a regression qualifies. Everything else -- including a real but non-blocking "+
			"defect -- is advisory.\n\n[%s] %s (%s:%d)\n%s\n\n"+
			"Call classify_blocking exactly once, then call finish with status \"passed\".",
		f.Severity, f.Title, f.File, f.LineStart, f.Body,
	)
	res, err := runPRReviewAgentRecorded(ctx, tc, tc.prReview.snapshotDir, "merge-gate", "classification", mission, 6, []agentexec.CaptureTool{{
		Name: "classify_blocking", Description: "Record the blocking verdict. Call exactly once, before finish.",
		Parameters: mergeGateSchema, RequiredFields: []string{"blocking"}, MaxCalls: 1,
	}})
	if err != nil || res.Status != agentexec.StatusPassed {
		return false //nolint:nilerr // a merge-gate call that could not run leaves the finding advisory, per the PRD
	}
	calls := res.Captures["classify_blocking"]
	if len(calls) != 1 {
		return false
	}
	var captured struct {
		Blocking bool `json:"blocking"`
	}
	if json.Unmarshal(calls[0], &captured) != nil {
		return false
	}
	return captured.Blocking
}
