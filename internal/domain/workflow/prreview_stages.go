package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// PRSource fetches a pull request under review: its descriptive content, its
// diff, and a read-only, .git-free snapshot of its head. It is Trees'
// counterpart for the pr-review workflow -- Trees reaches the task's own
// worktree, which the pipeline must never read (docs/prds/pr-review-agent.md,
// Isolation); PRSource reaches the arbitrary pull request named by
// Task.Owner/Repo/PRNumber instead, and carries no push credential.
//
// The real forge-backed implementation belongs to whichever bead wires
// pr-review's triggers (archie-core-afbk.7); this bead builds and tests the
// Stage wiring against a fake.
type PRSource interface {
	// Metadata returns the pull request's title, body and commit messages --
	// phase 1's "PR metadata" and phase 3's hallucination-check dimension's
	// "what the PR claims".
	Metadata(ctx context.Context, owner, repo string, number int) (PRMetadata, error)
	// Diff returns the pull request's unified diff against its base branch.
	Diff(ctx context.Context, owner, repo string, number int) (string, error)
	// Snapshot materializes the pull request head's tracked files into
	// destDir with no .git directory present, and returns the head commit
	// SHA those files (and the diff's line numbers) were measured on.
	Snapshot(ctx context.Context, owner, repo string, number int, destDir string) (headSHA string, err error)
}

// PRMetadata is a pull request's descriptive content.
type PRMetadata struct {
	Title   string
	Body    string
	Commits []string // commit messages, oldest first
}

// prReviewState is the pr-review workflow's cross-stage scratch state: each
// phase's output for the phases after it to consume. Unexported, following
// the pattern the feasibility workflow's decision field set.
type prReviewState struct {
	metadata    PRMetadata
	diff        string
	files       []prreview.FileChange
	stats       prreview.DiffStats
	depth       prreview.Depth
	aiGenerated float64

	snapshotDir string
	headSHA     string
	blastRadius []string
	// clusters and highExposure are phase 6's coverage-gate inputs, computed
	// once in anatomy alongside blastRadius: clusters groups changed files by
	// directory, highExposure is the blastRadius files more than one changed
	// file reaches.
	clusters     []prreview.Cluster
	highExposure []string

	narrative        string
	dimensions       []prreview.Dimension
	findings         []prreview.Finding
	scored           []prreview.ScoredFinding
	reviewerFailures atomic.Int64

	// findingsMu guards findings during phase 6, where the coverage gate and
	// consistency verification append to it from two goroutines running in
	// parallel (docs/prds/pr-review-agent.md, phase 6). Every other phase
	// touches findings from a single goroutine and does not need it.
	findingsMu sync.Mutex

	// runStart is when phase 1 started, the origin every budget-share check
	// measures elapsed wall-clock against.
	runStart time.Time
	// tokensSpent is the run's cumulative token spend so far, across every
	// agent call any phase has made. It is an atomic because phases 3, 4 and
	// 9 add to it from concurrent goroutines.
	tokensSpent atomic.Int64
	// skippedPhases names every phase a budget check skipped, in the order
	// they were skipped, so phase 9's output can report them (docs/prds/
	// pr-review-agent.md, Budget: "The posted review names every skipped
	// phase, so an exhausted run never reads as a clean one"). Every phase
	// runs sequentially in the workflow engine's stage loop, so appends here
	// need no lock.
	skippedPhases []string
}

// prReviewTotalBudget bounds the whole pr-review run's cost (tokens, the
// only spend the agent runtime accounts for) and wall-clock, split evenly
// across prReviewBudgetPhases in pipeline order (docs/prds/pr-review-agent.md,
// Budget section). Sized generously: exhausting it is meant to catch a run
// that is genuinely stuck or unexpectedly expensive, not a typical one.
var prReviewTotalBudget = prreview.Budget{MaxTokens: 2_000_000, WallClock: 30 * time.Minute}

// prReviewBudgetPhases are the budget-tracked phases, in pipeline order.
// Intake is not tracked: it must always run, since the depth, the AI-score
// and the budget's own runStart all come from it, and every later phase and
// this list itself depend on its output existing.
var prReviewBudgetPhases = []string{"anatomy", "lenses", "review", "precision-gate", "verification", "coverage-consistency", "merge-gate", "output"}

// budgetExhausted reports whether phase has already spent its cumulative
// share of the run's total budget, checked once before that phase's
// expensive (agent-call) work starts. A phase absent from
// prReviewBudgetPhases is a bug in that list, not a runtime condition, and
// is never treated as exhausted.
func budgetExhausted(tc *TaskContext, phase string) bool {
	idx := slices.Index(prReviewBudgetPhases, phase)
	if idx < 0 {
		return false
	}
	// A zero runStart means the run's clock was never started (a stage
	// invoked directly against a hand-built prReviewState, as the stage unit
	// tests do) rather than a run that has been going since the epoch.
	var elapsed time.Duration
	if !tc.prReview.runStart.IsZero() {
		elapsed = time.Since(tc.prReview.runStart)
	}
	return prReviewTotalBudget.PhaseExhausted(idx+1, len(prReviewBudgetPhases),
		int(tc.prReview.tokensSpent.Load()), elapsed)
}

// skipPhase records phase as budget-skipped.
func skipPhase(tc *TaskContext, phase string) {
	tc.prReview.skippedPhases = append(tc.prReview.skippedPhases, phase)
}

// prReviewConcurrency bounds phases 3 (lenses), 4 (reviewers) and 9 (polish),
// all "in parallel, at most 8 at a time" per the PRD except phase 3, which
// never has more than three lenses to begin with.
const prReviewConcurrency = 8

// PRReview is the pull request reviewer: a fixed pipeline, not a single
// agent (docs/prds/pr-review-agent.md). archie-core-afbk.3 wired phases 1, 2,
// 3, 4, 7 and 9; archie-core-afbk.4 added 5 and 6 (verification,
// coverage/consistency); archie-core-afbk.5 added phase 8 (the merge gate)
// and the budget shares/skipped-phase reporting that run throughout;
// archie-core-afbk.8 adds the precision dial (between review and
// verification) and the operator-approval gate (between synthesis and the
// merge gate), both off by default and each gated by its own config flag.
//
// The operator-approval gate is this workflow's OWN stage-list entry, not a
// shared one (docs/prds/pr-review-operator-response.md): the implement
// workflow splices the shared decision stages, and a gate there would end an
// implement run before its PR exists.
func PRReview() Workflow {
	return Workflow{
		Name: "pr-review",
		// pr_number is required so a binding (watched-repos) or a chat task
		// (the operator trigger) can only target pr-review by actually
		// naming a pull request; stagePRIntake reads it from Task.Inputs
		// when Task.PRNumber is unset (archie's-own-PRs sets PRNumber
		// directly and never goes through binding validation, so this
		// declaration does not affect that path).
		Interface: task.WorkflowInterface{
			Inputs: map[string]task.InputSpec{
				"pr_number": {Type: "number", Required: true},
				"depth":     {Type: "string"},
			},
		},
		Stages: prReviewStandaloneStages(),
	}
}

// prReviewDecisionStages is phases 1 through 8: everything up to and
// including the merge gate's blocking/advisory verdict, but before phase 9
// posts anything, and WITHOUT the operator-approval gate. Split out from
// PRReview so the implement workflow can splice it in before StageOpenPR
// (archie-core-afbk.7's "archie's own PRs" trigger) -- and so the operator
// gate can never ride along on that splice
// (docs/prds/pr-review-operator-response.md, "The gate is not reachable from
// archie's own PRs"): there the gate's question (which findings to post) has
// no pull request to post to yet, and a park would end the implement run
// before it opened one (archie-core-7nst).
func prReviewDecisionStages() []Stage {
	return append(prReviewPipelineStages(), stagePRMergeGate())
}

// prReviewStandaloneStages is the standalone pr-review workflow's full stage
// list: the shared decision phases with the operator-approval gate inserted
// between synthesis and the merge gate -- the gate runs before the merge
// gate's verdict, so an operator's re-review never has to undo one computed
// on a since-changed finding set -- followed by phase 9's posting.
func prReviewStandaloneStages() []Stage {
	pipeline := prReviewPipelineStages()
	stages := make([]Stage, 0, len(pipeline)+3)
	stages = append(stages, pipeline...)
	return append(stages, stagePROperatorApproval(), stagePRMergeGate(), stagePROutput())
}

// prReviewPipelineStages is phases 1 through 7: intake through synthesis, the
// stages every pr-review trigger shares.
func prReviewPipelineStages() []Stage {
	return []Stage{
		stagePRIntake(),
		stagePRAnatomy(),
		stagePRLenses(),
		stagePRReview(),
		stagePRPrecisionGate(),
		stagePRVerification(),
		stagePRCoverageConsistency(),
		stagePRSynthesis(),
	}
}

// stagePRIntake is pipeline phase 1: PR metadata and diff statistics, the
// depth they classify, and the machine-written confidence phase 3's
// hallucination-check dimension and phase 7's scoring both read.
func stagePRIntake() Stage {
	return Stage{Name: "intake", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.PRSource == nil {
			return fmt.Errorf("pr-review: no PRSource configured")
		}
		// A watched-repositories binding or the operator chat trigger
		// assigns the pull request through the declared pr_number input,
		// not PRNumber directly; the daemon's container-acquisition path
		// already resolves this before dispatch, but an in-process or
		// subprocess run that bypasses it still needs it resolved here.
		// Archie's own PRs (Task.Workflow == "implement", embedding this
		// stage through StagePRReviewAndOpenPR) legitimately has no PR
		// number at all -- localPRSource ignores it, since the PR does not
		// exist yet -- so the missing-number error only applies to the
		// standalone pr-review workflow, which always needs a real,
		// externally-fetchable pull request to review.
		if tc.Task.PRNumber == 0 {
			tc.Task.PRNumber = tc.Task.EffectivePRNumber()
		}
		if tc.Task.PRNumber == 0 && tc.Task.Workflow == "pr-review" {
			return fmt.Errorf("pr-review: no pull request number (set directly or via the pr_number input)")
		}
		var explicit prreview.Depth
		if value, supplied := tc.Task.Inputs["depth"]; supplied {
			depth, ok := value.(string)
			if !ok || (depth != "quick" && depth != "standard" && depth != "deep") {
				return fmt.Errorf("pr-review: depth must be quick, standard or deep")
			}
			explicit = prreview.Depth(depth)
		}
		meta, err := tc.PRSource.Metadata(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber)
		if err != nil {
			return fmt.Errorf("fetch pull request metadata: %w", err)
		}
		diff, err := tc.PRSource.Diff(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber)
		if err != nil {
			return fmt.Errorf("fetch pull request diff: %w", err)
		}
		files := prreview.ParseDiff(diff)
		stats := prreview.SummarizeFiles(files)
		depth := prreview.ResolveDepth(stats.TotalAdditions+stats.TotalDeletions, explicit)

		// tc.prReview must exist before scoreAIGenerated runs: every
		// pr-review agent call, including this one, records its token spend
		// against tc.prReview.tokensSpent.
		tc.prReview = &prReviewState{
			metadata: meta, diff: diff, files: files, stats: stats,
			depth: depth, runStart: time.Now(),
		}

		aiGenerated, err := scoreAIGenerated(ctx, tc, meta)
		if err != nil {
			return fmt.Errorf("score likely machine-written PR: %w", err)
		}
		tc.prReview.aiGenerated = aiGenerated
		return nil
	}}
}

// scoreAIGenerated runs phase 1's classification-role call: how confident an
// agent is that the PR's own description reads as machine-written, which
// phase 3's hallucination-check dimension and phase 7's scoring both use.
func scoreAIGenerated(ctx context.Context, tc *TaskContext, meta PRMetadata) (float64, error) {
	// This call reads no files -- only the metadata already in its mission --
	// so it gets its own empty scratch directory rather than the pull
	// request's snapshot, which does not exist yet at phase 1.
	scratch, err := os.MkdirTemp("", "pr-review-intake-*")
	if err != nil {
		return 0, fmt.Errorf("create intake scratch directory: %w", err)
	}
	defer os.RemoveAll(scratch)

	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"confidence": {"type": "number", "description": "0 = clearly written by the human author, 1 = clearly machine-generated."},
			"reasons": {"type": "string", "description": "What in the title, body or commit messages drove the confidence."}
		},
		"required": ["confidence"]
	}`)
	mission := fmt.Sprintf(
		"Judge whether this pull request's own description reads as written by an AI "+
			"coding assistant rather than the human who opened it: generic boilerplate, a "+
			"summary that oversells the diff, or phrasing typical of an LLM's own commit "+
			"or PR style. This is about the PR's description, not the code.\n\n"+
			"Title: %s\n\nDescription:\n%s\n\nCommit messages:\n%s\n\n"+
			"Call score_ai_generated exactly once with your confidence, then call finish "+
			"with status \"passed\".",
		meta.Title, meta.Body, strings.Join(meta.Commits, "\n"),
	)
	res, err := runPRReviewAgent(ctx, tc, scratch, "intake-ai-score", "classification", mission, 6, []agentexec.CaptureTool{{
		Name: "score_ai_generated", Description: "Record the machine-written confidence. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"confidence"}, MaxCalls: 1,
	}})
	if err != nil {
		return 0, err
	}
	calls := res.Captures["score_ai_generated"]
	if len(calls) != 1 {
		return 0, fmt.Errorf("intake-ai-score called score_ai_generated %d times (want exactly once)", len(calls))
	}
	var captured struct {
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(calls[0], &captured); err != nil {
		return 0, fmt.Errorf("decode machine-written confidence: %w", err)
	}
	return captured.Confidence, nil
}

// stagePRAnatomy is pipeline phase 2: the read-only snapshot every later
// phase reads from, the deterministic clustering and blast radius, and one
// agent's narrative of the change.
func stagePRAnatomy() Stage {
	return Stage{Name: "anatomy", Run: func(ctx context.Context, tc *TaskContext) error {
		dir, err := os.MkdirTemp("", fmt.Sprintf("pr-review-snapshot-task%d-*", tc.Task.ID))
		if err != nil {
			return fmt.Errorf("create pull request snapshot directory: %w", err)
		}
		tc.prReview.snapshotDir = dir
		headSHA, err := tc.PRSource.Snapshot(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber, dir)
		if err != nil {
			return fmt.Errorf("snapshot pull request head: %w", err)
		}
		tc.prReview.headSHA = headSHA

		changed := make([]string, 0, len(tc.prReview.files))
		for _, f := range tc.prReview.files {
			changed = append(changed, f.Path)
		}
		blast, err := prreview.BlastRadius(os.DirFS(dir), changed)
		if err != nil {
			return fmt.Errorf("compute blast radius: %w", err)
		}
		tc.prReview.blastRadius = blast
		tc.prReview.clusters = prreview.ClusterFiles(tc.prReview.files)
		exposure, err := prreview.ExposureCounts(os.DirFS(dir), changed)
		if err != nil {
			return fmt.Errorf("compute exposure counts: %w", err)
		}
		tc.prReview.highExposure = prreview.HighExposureFiles(exposure)

		mission := fmt.Sprintf(
			"Read this pull request's diff and enough of the surrounding repository to "+
				"write a short narrative of the change: what it does, the riskiest surfaces "+
				"it touches, any change that does not fit the PR's own description, and any "+
				"gap between what the description claims and what the diff carries out.\n\n"+
				"Title: %s\n\nDescription:\n%s\n\nDiff:\n%s\n\n"+
				"Call finish with status \"passed\" and your narrative as the summary.",
			tc.prReview.metadata.Title, tc.prReview.metadata.Body, clip(tc.prReview.diff, 60000),
		)
		if budgetExhausted(tc, "anatomy") {
			skipPhase(tc, "anatomy")
			return nil
		}
		res, err := runPRReviewAgent(ctx, tc, tc.prReview.snapshotDir, "anatomy", "review", mission, 15, nil)
		if err != nil {
			return err
		}
		tc.prReview.narrative = res.Summary
		return nil
	}}
}

// prReviewLenses are phase 3's three fixed lenses. Each proposes review
// dimensions from a different angle; code merges, dedups and caps the result.
var prReviewLenses = []struct {
	name   string
	prompt string
}{
	{name: "behaviour", prompt: "where the old and new code diverge for any input: API contracts, concurrency and state, security, error handling, data flow"},
	{name: "mechanics", prompt: "whether the code runs at the language and framework level: types, signatures and every caller, decorators and middleware, framework contracts, imports"},
	{name: "fit", prompt: "patterns, complexity, abstraction, test adequacy, documentation that no longer matches, dependencies, migration completeness"},
}

// stagePRLenses is pipeline phase 3: three lens agents proposing review
// dimensions in parallel, merged with the hallucination-check dimension (for
// a likely machine-written PR) as one more candidate, deduplicated and
// capped by code. The hallucination dimension competes for the same cap as
// everything else -- "at most N dimensions" is a total, not a total plus one
// -- so it is added before MergeDimensions cuts the tail, never after.
func stagePRLenses() Stage {
	return Stage{Name: "lenses", Run: func(ctx context.Context, tc *TaskContext) error {
		if budgetExhausted(tc, "lenses") {
			skipPhase(tc, "lenses")
			return nil
		}
		outputs := make([][]prreview.Dimension, len(prReviewLenses))
		errs := make([]error, len(prReviewLenses))
		forEachBounded(len(prReviewLenses), len(prReviewLenses), func(i int) {
			outputs[i], errs[i] = runLens(ctx, tc, prReviewLenses[i].name, prReviewLenses[i].prompt)
		})
		for i, err := range errs {
			if err != nil {
				return fmt.Errorf("lens %s: %w", prReviewLenses[i].name, err)
			}
		}
		candidates := outputs
		if extra := prreview.HallucinationDimension(tc.prReview.aiGenerated); extra != nil {
			candidates = append(append([][]prreview.Dimension{}, outputs...), []prreview.Dimension{*extra})
		}
		tc.prReview.dimensions = prreview.MergeDimensions(candidates, prreview.MaxDimensionsFor(tc.prReview.depth))
		return nil
	}}
}

// runLens runs one phase-3 lens call and decodes its proposed dimensions.
func runLens(ctx context.Context, tc *TaskContext, name, angle string) ([]prreview.Dimension, error) {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"dimensions": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"prompt": {"type": "string", "description": "The reviewer's mission for this dimension, written for this PR."},
						"target_files": {"type": "array", "items": {"type": "string"}},
						"context_files": {"type": "array", "items": {"type": "string"}},
						"priority": {"type": "number"}
					},
					"required": ["name", "prompt", "target_files", "priority"]
				}
			}
		},
		"required": ["dimensions"]
	}`)
	mission := fmt.Sprintf(
		"You are the %s lens over this pull request: %s.\n\n"+
			"Title: %s\n\nDescription:\n%s\n\nDiff:\n%s\n\n"+
			"Propose the review dimensions this angle needs, each with a reviewer prompt "+
			"written for this specific PR, its target files, any context files a reviewer "+
			"should also read, and a priority (higher runs first when the depth caps how "+
			"many dimensions survive). Call propose_dimensions exactly once, then call "+
			"finish with status \"passed\".",
		name, angle, tc.prReview.metadata.Title, tc.prReview.metadata.Body, clip(tc.prReview.diff, 60000),
	)
	res, err := runPRReviewAgent(ctx, tc, tc.prReview.snapshotDir, "lens-"+name, "review", mission, 15, []agentexec.CaptureTool{{
		Name: "propose_dimensions", Description: "Record this lens's proposed review dimensions. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"dimensions"}, MaxCalls: 1,
	}})
	if err != nil {
		return nil, err
	}
	calls := res.Captures["propose_dimensions"]
	if len(calls) != 1 {
		return nil, fmt.Errorf("lens %s called propose_dimensions %d times (want exactly once)", name, len(calls))
	}
	var captured struct {
		Dimensions []struct {
			Name         string   `json:"name"`
			Prompt       string   `json:"prompt"`
			TargetFiles  []string `json:"target_files"`
			ContextFiles []string `json:"context_files"`
			Priority     float64  `json:"priority"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(calls[0], &captured); err != nil {
		return nil, fmt.Errorf("decode lens %s dimensions: %w", name, err)
	}
	out := make([]prreview.Dimension, len(captured.Dimensions))
	for i, d := range captured.Dimensions {
		out[i] = prreview.Dimension{
			Name: d.Name, Prompt: d.Prompt, TargetFiles: d.TargetFiles,
			ContextFiles: d.ContextFiles, Priority: d.Priority,
		}
	}
	return out, nil
}

// stagePRReview is pipeline phase 4: one reviewer call per dimension, at most
// prReviewConcurrency at a time. A reviewer that exhausts its turn cap or
// deadline before its terminal tool call records StepFailed -- distinct from
// StepSucceeded for a reviewer that completed and found nothing -- so
// synthesis and the posted review can tell "unreviewed" from "reviewed-clean"
// (docs/prds/pr-review-agent.md, phase 4).
func stagePRReview() Stage {
	return Stage{Name: "review", Run: func(ctx context.Context, tc *TaskContext) error {
		if budgetExhausted(tc, "review") {
			skipPhase(tc, "review")
			return nil
		}
		dimensions := tc.prReview.dimensions
		findings := make([][]prreview.Finding, len(dimensions))
		forEachBounded(prReviewConcurrency, len(dimensions), func(i int) {
			findings[i] = runReviewer(ctx, tc, dimensions[i])
		})
		var all []prreview.Finding
		for _, f := range findings {
			all = append(all, f...)
		}
		tc.prReview.findings = all
		return nil
	}}
}

// runReviewer runs one phase-4 reviewer call. It never returns a Go error to
// its caller: a reviewer that failed, or did not reach its terminal tool
// call, contributes no findings -- the same zero-findings shape a reviewer
// that ran cleanly and found nothing produces. The two are not confused in
// the record, only in this return value: runReviewerAgent gives the first
// case a distinct StepFailed child step, so a reader of the execution tree
// (not this slice) can tell "unreviewed" from "reviewed-clean" apart.
func runReviewer(ctx context.Context, tc *TaskContext, dim prreview.Dimension) []prreview.Finding {
	mission := fmt.Sprintf(
		"%s\n\nTarget files: %s\n\nRead the target files (and, if useful, the context "+
			"files) in the snapshot and report every finding for this dimension. Quote the "+
			"exact evidence for each finding; do not report anything you cannot point at in "+
			"the code. Call report_findings exactly once (an empty array is a valid, "+
			"complete report), then call finish with status \"passed\".",
		dim.Prompt, strings.Join(dim.TargetFiles, ", "),
	)
	name := "reviewer-" + dim.Name
	res, runErr := runReviewerAgent(ctx, tc, name, mission, []agentexec.CaptureTool{reportFindingsTool})
	if runErr != nil || res.Status != agentexec.StatusPassed {
		tc.prReview.reviewerFailures.Add(1)
		return nil
	}
	findings, err := decodeReportedFindings(res.Captures["report_findings"], dim.Name)
	if err != nil {
		tc.prReview.reviewerFailures.Add(1)
		return nil
	}
	return findings
}

// reportFindingsSchema is the finding shape every pr-review agent call that
// reports findings captures them in: phase 4's reviewer, phase 6's gap
// reviewers (which reuse runReviewer directly) and consistency verification.
var reportFindingsSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"findings": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"file": {"type": "string"},
					"line_start": {"type": "integer"},
					"line_end": {"type": "integer"},
					"severity": {"type": "string", "enum": ["critical", "important", "suggestion", "nitpick"]},
					"title": {"type": "string"},
					"body": {"type": "string"},
					"suggestion": {"type": "string"},
					"evidence": {"type": "string"},
					"confidence": {"type": "number"},
					"tags": {"type": "array", "items": {"type": "string"}}
				},
				"required": ["file", "line_start", "severity", "title", "body", "evidence", "confidence"]
			}
		}
	},
	"required": ["findings"]
}`)

// reportFindingsTool is the capture tool every findings-reporting call
// registers, at the shape reportFindingsSchema names.
var reportFindingsTool = agentexec.CaptureTool{
	Name: "report_findings", Description: "Record findings. Call exactly once, before finish.",
	Parameters: reportFindingsSchema, RequiredFields: []string{"findings"}, MaxCalls: 1,
}

// decodeReportedFindings decodes one report_findings capture into findings
// tagged with dimension, shared by every call site reportFindingsSchema
// backs. A call that never reported (no captures) or reported malformed JSON
// decodes to no findings and an error the caller treats as "nothing to add",
// not as a stage failure -- an agent that could not report is not evidence
// the code that follows should stop.
func decodeReportedFindings(calls []json.RawMessage, dimension string) ([]prreview.Finding, error) {
	if len(calls) != 1 {
		return nil, fmt.Errorf("report_findings called %d times (want exactly once)", len(calls))
	}
	var captured struct {
		Findings []struct {
			File       string   `json:"file"`
			LineStart  int      `json:"line_start"`
			LineEnd    int      `json:"line_end"`
			Severity   string   `json:"severity"`
			Title      string   `json:"title"`
			Body       string   `json:"body"`
			Suggestion string   `json:"suggestion"`
			Evidence   string   `json:"evidence"`
			Confidence float64  `json:"confidence"`
			Tags       []string `json:"tags"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(calls[0], &captured); err != nil {
		return nil, fmt.Errorf("decode findings: %w", err)
	}
	out := make([]prreview.Finding, len(captured.Findings))
	for i, f := range captured.Findings {
		lineEnd := f.LineEnd
		if lineEnd == 0 {
			lineEnd = f.LineStart
		}
		out[i] = prreview.Finding{
			Dimension: dimension, File: f.File, LineStart: f.LineStart, LineEnd: lineEnd,
			Severity: prreview.Severity(f.Severity), Title: f.Title, Body: f.Body,
			Suggestion: f.Suggestion, Evidence: f.Evidence, Confidence: f.Confidence, Tags: f.Tags,
		}
	}
	return out, nil
}

// prReviewMaxSteps bounds one reviewer's tool-loop iterations. Exhausting it
// (or the wall-clock budget) before the terminal finish call is exactly the
// "turn cap or deadline" case the phase-4 status distinction exists for.
const prReviewMaxSteps = 25

// runReviewerAgent runs one phase-4 reviewer call and records its child step
// with the phase-4 status distinction (docs/prds/pr-review-agent.md, phase
// 4): StepFailed for a reviewer that exhausted its turn cap or deadline
// before its terminal tool call, StepSucceeded for one that reached it --
// whether or not it reported any findings. tc.RunAgentChild does not serve
// this: it maps only the Go error, so a "parked" (turn-cap/deadline) result
// with no Go error would record StepSucceeded exactly like a clean,
// zero-findings run, and the two would be indistinguishable to anything
// reading the execution tree afterwards.
func runReviewerAgent(
	ctx context.Context, tc *TaskContext, name, mission string, captureTools []agentexec.CaptureTool,
) (agentexec.Result, error) {
	modelRef := tc.Cfg.Models["review"]
	if modelRef == "" {
		return agentexec.Result{}, fmt.Errorf("no model configured for role %q (set [models] in config)", "review")
	}
	req := agentexec.Request{
		Version: agentexec.ProtocolVersion, TaskID: tc.Task.ID, Attempt: tc.Task.Attempt,
		Stage: name, Workflow: tc.Task.Workflow, Model: modelRef,
		ContextWindow: modelContextBudget(tc.Cfg, modelRef),
		Mission:       mission, ReadOnly: true,
		Budget:       agentexec.Budget{MaxSteps: prReviewMaxSteps, WallClock: tc.Cfg.Budgets.WallClock.Std()},
		CaptureTools: captureTools,
	}
	stepID, _, err := tc.startChildStep(ctx, task.StepKindAgent, name)
	if err != nil {
		return agentexec.Result{}, err
	}
	res, runErr := tc.Agent.Run(ctx, tc.prReview.snapshotDir, req, tc.toolCallReporter(name))
	tc.prReview.tokensSpent.Add(int64(res.TokensUsed))
	to, detail := taskstate.StepSucceeded, res.Summary
	switch {
	case runErr != nil:
		to, detail = taskstate.StepFailed, runErr.Error()
	case res.Status != agentexec.StatusPassed:
		to, detail = taskstate.StepFailed, fmt.Sprintf("reviewer did not complete (%s: %s)", res.Status, res.StopReason)
	}
	if ferr := tc.finishChildStep(ctx, stepID, to, detail, int64(res.TokensUsed)); ferr != nil {
		return res, ferr
	}
	tc.prReview.tokensSpent.Add(int64(res.TokensUsed))
	return res, runErr
}

// stagePRSynthesis is pipeline phase 7, code only: score, drop findings
// under their severity's confidence floor, merge duplicates, rank, and cap
// at the inline comment limit. Phase 8 (the merge gate) runs after this
// stage, so every surviving finding's Blocking flag is still whatever phase
// 5/6 left it as (false, since neither sets it) until that stage decides.
func stagePRSynthesis() Stage {
	return Stage{Name: "synthesis", Run: func(_ context.Context, tc *TaskContext) error {
		scored := prreview.Score(tc.prReview.findings, prreview.ScoreInputs{
			AIGenerated:      tc.prReview.aiGenerated,
			BlastRadiusFiles: len(tc.prReview.blastRadius),
		})
		tc.prReview.scored = prreview.CapInlineComments(scored)
		return nil
	}}
}

// stagePROutput is pipeline phase 9: one polish call per comment, at most
// prReviewConcurrency at a time, keeping the original wording on failure,
// then posting the surviving inline-anchored findings as one forge review.
// The polish pass is what a budget-exhausted run skips; posting itself
// always runs, since it is the pipeline's only externally visible act and
// must report what happened even when every other phase was skipped.
//
// Used by the standalone pr-review workflow only. Archie's own PRs trigger
// (StagePRReviewAndOpenPR, prreview_own_pr.go) needs the same polish-then-post
// work but must not overwrite the StatusPROpen outcome OpenPR already set, so
// it calls runPROutputPhase directly instead of this Stage.
func stagePROutput() Stage {
	return Stage{Name: "output", Run: func(ctx context.Context, tc *TaskContext) error {
		detail, err := runPROutputPhase(ctx, tc)
		if err != nil {
			return err
		}
		tc.Outcome = Outcome{Status: StatusCompleted, Detail: detail}
		return nil
	}}
}

// runPROutputPhase is phase 9's actual work, factored out of stagePROutput so
// a caller that must preserve its own outcome (archie's own PRs, which has
// already set StatusPROpen by the time findings are ready to post) can run it
// without stagePROutput's unconditional StatusCompleted assignment.
func runPROutputPhase(ctx context.Context, tc *TaskContext) (string, error) {
	if budgetExhausted(tc, "output") {
		skipPhase(tc, "output")
	} else {
		polished := make([]prreview.ScoredFinding, len(tc.prReview.scored))
		forEachBounded(prReviewConcurrency, len(tc.prReview.scored), func(i int) {
			polished[i] = polishFinding(ctx, tc, tc.prReview.scored[i])
		})
		tc.prReview.scored = polished
	}

	if err := postPRReview(ctx, tc); err != nil {
		return "", fmt.Errorf("post review: %w", err)
	}
	detail := fmt.Sprintf("posted %d finding(s) as %s", len(tc.prReview.scored), prreview.ReviewEventFor(tc.prReview.scored))
	if len(tc.prReview.skippedPhases) > 0 {
		detail += fmt.Sprintf("; skipped: %s", strings.Join(tc.prReview.skippedPhases, ", "))
	}
	return detail, nil
}

// polishFinding runs one phase-9 polish call, tightening a finding's wording.
// It keeps the original finding unchanged on any failure, per the PRD.
func polishFinding(ctx context.Context, tc *TaskContext, f prreview.ScoredFinding) prreview.ScoredFinding {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {"body": {"type": "string", "description": "The tightened finding body."}},
		"required": ["body"]
	}`)
	mission := fmt.Sprintf(
		"Tighten the wording of this review comment without changing its meaning or "+
			"dropping any concrete detail (file, line, failure scenario).\n\n"+
			"Title: %s\nBody: %s\n\nCall polish exactly once with the tightened body, then "+
			"call finish with status \"passed\".",
		f.Title, f.Body,
	)
	res, err := runPRReviewAgent(ctx, tc, tc.prReview.snapshotDir, "polish", "classification", mission, 4, []agentexec.CaptureTool{{
		Name: "polish", Description: "Record the tightened comment body. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"body"}, MaxCalls: 1,
	}})
	if err != nil {
		return f
	}
	calls := res.Captures["polish"]
	if len(calls) != 1 {
		return f
	}
	var captured struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(calls[0], &captured); err != nil || captured.Body == "" {
		return f
	}
	f.Body = captured.Body
	return f
}

// postPRReview posts every line-anchored finding as one forge review. A
// finding with no line (LineStart <= 0) is dropped rather than posted
// anywhere -- docs/prds/inline-review.md's PR-body fallback list for
// whole-file findings, and REQUEST_CHANGES vs COMMENT event submission
// (workflow.Forger.CreateReviewComments always posts a COMMENT-state
// review; GitHub's implementation does not submit a review object at all,
// only per-comment calls, so REQUEST_CHANGES needs a Forger/forge change,
// not just a caller change here) are both real gaps this bead does not
// close; see the follow-up bead this bead's commit files.
func postPRReview(ctx context.Context, tc *TaskContext) error {
	comments := make([]ReviewComment, 0, len(tc.prReview.scored))
	for _, f := range tc.prReview.scored {
		if f.LineStart <= 0 {
			continue
		}
		comments = append(comments, ReviewComment{Path: f.File, Line: f.LineStart, Body: f.Body})
	}
	if len(comments) == 0 {
		return nil
	}
	return tc.Forge.CreateReviewComments(
		ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber, tc.prReview.headSHA, comments,
	)
}

// runPRReviewAgent runs one read-only agent call rooted at workspace -- never
// tc.Dir, per Isolation -- and discards the run/park distinction: any
// non-passed result is a stage error. Every pr-review phase but the reviewer
// fan-out (which must tell "did not finish" from "finished, found nothing"
// apart) uses this. Every phase from anatomy onward passes
// tc.prReview.snapshotDir; intake's AI-score call runs before that snapshot
// exists, so it passes its own scratch workspace instead.
func runPRReviewAgent(
	ctx context.Context, tc *TaskContext, workspace, name, role, mission string, maxSteps int, captureTools []agentexec.CaptureTool,
) (agentexec.Result, error) {
	res, err := runPRReviewAgentRecorded(ctx, tc, workspace, name, role, mission, maxSteps, captureTools)
	if err != nil {
		return res, err
	}
	if res.Status != agentexec.StatusPassed {
		detail := res.Detail
		if detail == "" {
			detail = res.Summary
		}
		return res, fmt.Errorf("%s %s (%s): %s", name, res.Status, res.StopReason, clip(detail, 2000))
	}
	return res, nil
}

// runPRReviewAgentRecorded is the shared request-building and StepExecution
// recording underneath every pr-review agent call. It returns whatever the
// runtime returned, status and all: callers that must distinguish "did not
// finish" from "finished, found nothing" (the reviewer fan-out) read
// res.Status themselves instead of getting an error for anything but a Go
// error.
func runPRReviewAgentRecorded(
	ctx context.Context, tc *TaskContext, workspace, name, role, mission string, maxSteps int, captureTools []agentexec.CaptureTool,
) (agentexec.Result, error) {
	modelRef := tc.Cfg.Models[role]
	if modelRef == "" {
		return agentexec.Result{}, fmt.Errorf("no model configured for role %q (set [models] in config)", role)
	}
	req := agentexec.Request{
		Version: agentexec.ProtocolVersion, TaskID: tc.Task.ID, Attempt: tc.Task.Attempt,
		Stage: name, Workflow: tc.Task.Workflow, Model: modelRef,
		ContextWindow: modelContextBudget(tc.Cfg, modelRef),
		Mission:       mission, ReadOnly: true,
		Budget:       agentexec.Budget{MaxSteps: maxSteps, WallClock: tc.Cfg.Budgets.WallClock.Std()},
		CaptureTools: captureTools,
	}
	res, err := tc.RunAgentChild(ctx, name, func() (agentexec.Result, error) {
		return tc.Agent.Run(ctx, workspace, req, tc.toolCallReporter(name))
	})
	tc.prReview.tokensSpent.Add(int64(res.TokensUsed))
	return res, err
}

// forEachBounded runs fn(0), fn(1), ..., fn(n-1), at most limit at a time,
// and waits for all of them. It is phase 3, 4 and 9's shared concurrency
// bound: tc.RunAgentChild already supports concurrent fan-out from inside
// one stage body (each call independently records a StepExecution child), so
// this needs only a semaphore, not an engine change.
func forEachBounded(limit, n int, fn func(i int)) {
	if n == 0 {
		return
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
