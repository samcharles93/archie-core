package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// fakePRSource is a test double for PRSource: no forge call, just canned
// metadata, diff and a snapshot copied from an in-memory file set.
type fakePRSource struct {
	metadata PRMetadata
	diff     string
	files    map[string]string // relative path -> content, written into the snapshot dir
	headSHA  string
	err      error
}

func (f *fakePRSource) Metadata(context.Context, string, string, int) (PRMetadata, error) {
	return f.metadata, f.err
}

func (f *fakePRSource) Diff(context.Context, string, string, int) (string, error) {
	return f.diff, f.err
}

func (f *fakePRSource) Snapshot(_ context.Context, _, _ string, _ int, destDir string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	for path, content := range f.files {
		if err := os.WriteFile(destDir+"/"+path, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return f.headSHA, nil
}

// concurrentAgentRunner counts simultaneous Run calls and dispatches by the
// request's Stage name -- pr-review names every child step after its own
// phase/dimension, so tests can script one behaviour per call site.
type concurrentAgentRunner struct {
	mu      sync.Mutex
	current int
	peak    int

	byStage func(stage string) (agentexec.Result, error)
}

func (r *concurrentAgentRunner) Run(
	_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter,
) (agentexec.Result, error) {
	r.mu.Lock()
	r.current++
	if r.current > r.peak {
		r.peak = r.current
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.current--
		r.mu.Unlock()
	}()
	res, err := r.byStage(req.Stage)
	res.Version, res.TaskID, res.Attempt, res.Stage = agentexec.ProtocolVersion, req.TaskID, req.Attempt, req.Stage
	return res, err
}

// threadSafeStore is a Store fake safe for the concurrent StepExecution
// writes phase 3, 4 and 9's bounded fan-out makes from goroutines --
// recordingStore (changes_test.go) is not, and races under -race.
type threadSafeStore struct {
	mu       sync.Mutex
	nextID   int64
	starts   []StepStart
	finishes []StepFinish
}

func (s *threadSafeStore) Update(context.Context, *Task) error { return nil }
func (s *threadSafeStore) Transition(context.Context, int64, string, string, string) error {
	return nil
}

func (s *threadSafeStore) InsertEvent(context.Context, events.Event) (int64, error) { return 0, nil }

func (s *threadSafeStore) StartStep(_ context.Context, start StepStart) (int64, events.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.starts = append(s.starts, start)
	return s.nextID, events.Event{}, nil
}

func (s *threadSafeStore) FinishStep(_ context.Context, finish StepFinish) (events.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishes = append(s.finishes, finish)
	return events.Event{}, nil
}

func passedResult(summary string) agentexec.Result {
	return agentexec.Result{Status: agentexec.StatusPassed, Summary: summary}
}

func captureResult(tool string, payload any) agentexec.Result {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return agentexec.Result{
		Status: agentexec.StatusPassed, Summary: "ok",
		Captures: map[string][]json.RawMessage{tool: {data}},
	}
}

func baseTaskContext(t *testing.T) *TaskContext {
	t.Helper()
	return &TaskContext{
		Task: &Task{ID: 1, Attempt: 1, Owner: "acme", Repo: "widgets", PRNumber: 42, Workflow: "pr-review"},
		Cfg: config.Config{
			Models: map[string]string{"review": "provider/review", "classification": "provider/classification"},
		},
		Store: &threadSafeStore{},
		Log:   slog.New(slog.DiscardHandler),
	}
}

func TestStagePRIntakeClassifiesDepthAndScoresAIGenerated(t *testing.T) {
	tc := baseTaskContext(t)
	tc.PRSource = &fakePRSource{
		metadata: PRMetadata{Title: "t", Body: "b"},
		diff:     smallDiff,
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return captureResult("score_ai_generated", map[string]any{"confidence": 0.9}), nil
	}}

	if err := stagePRIntake().Run(context.Background(), tc); err != nil {
		t.Fatalf("intake: %v", err)
	}
	if tc.prReview == nil {
		t.Fatal("intake left no prReview state")
	}
	if tc.prReview.depth != prreview.DepthQuick {
		t.Errorf("depth = %q, want quick for a %d-line diff", tc.prReview.depth, tc.prReview.stats.TotalAdditions+tc.prReview.stats.TotalDeletions)
	}
	if tc.prReview.aiGenerated != 0.9 {
		t.Errorf("aiGenerated = %v, want 0.9 (from the captured score)", tc.prReview.aiGenerated)
	}
}

func TestStagePRIntakeFailsWithNoPRSource(t *testing.T) {
	tc := baseTaskContext(t)
	if err := stagePRIntake().Run(context.Background(), tc); err == nil {
		t.Fatal("want an error with no PRSource configured")
	}
}

func TestStagePRIntakeResolvesPRNumberFromInputsWhenUnset(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Task.PRNumber = 0
	tc.Task.Inputs = map[string]any{"pr_number": json.Number("77")}
	tc.PRSource = &fakePRSource{metadata: PRMetadata{Title: "t", Body: "b"}, diff: smallDiff}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return captureResult("score_ai_generated", map[string]any{"confidence": 0.1}), nil
	}}

	if err := stagePRIntake().Run(context.Background(), tc); err != nil {
		t.Fatalf("intake: %v", err)
	}
	if tc.Task.PRNumber != 77 {
		t.Errorf("PRNumber = %d, want 77 (resolved from Inputs)", tc.Task.PRNumber)
	}
}

func TestStagePRIntakeFailsWithNoPRNumberAnywhere(t *testing.T) {
	tc := baseTaskContext(t)
	tc.Task.PRNumber = 0
	tc.PRSource = &fakePRSource{metadata: PRMetadata{Title: "t", Body: "b"}, diff: smallDiff}
	if err := stagePRIntake().Run(context.Background(), tc); err == nil {
		t.Fatal("want an error with no PR number set directly or via Inputs")
	}
}

func TestStagePRIntakeAllowsNoPRNumberWhenEmbeddedInAnotherWorkflow(t *testing.T) {
	// Archie's own PRs (StagePRReviewAndOpenPR) embeds this stage inside the
	// implement workflow, reviewing a change with no PR number yet --
	// localPRSource ignores the number entirely. The missing-number error
	// must not fire here, only for the standalone pr-review workflow.
	tc := baseTaskContext(t)
	tc.Task.Workflow = "implement"
	tc.Task.PRNumber = 0
	tc.PRSource = &fakePRSource{metadata: PRMetadata{Title: "t", Body: "b"}, diff: smallDiff}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return captureResult("score_ai_generated", map[string]any{"confidence": 0.1}), nil
	}}

	if err := stagePRIntake().Run(context.Background(), tc); err != nil {
		t.Fatalf("intake: %v (archie's own PRs must not require a PR number)", err)
	}
}

func TestStagePRAnatomyBuildsSnapshotBlastRadiusAndNarrative(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		diff:  smallDiff,
		files: prreview.ParseDiff(smallDiff),
	}
	tc.PRSource = &fakePRSource{
		headSHA: "deadbeef",
		files:   map[string]string{"main.go": "package main\n"},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return passedResult("narrative here"), nil
	}}

	if err := stagePRAnatomy().Run(context.Background(), tc); err != nil {
		t.Fatalf("anatomy: %v", err)
	}
	if tc.prReview.headSHA != "deadbeef" {
		t.Errorf("headSHA = %q, want deadbeef", tc.prReview.headSHA)
	}
	if tc.prReview.snapshotDir == "" {
		t.Fatal("no snapshot directory recorded")
	}
	defer os.RemoveAll(tc.prReview.snapshotDir)
	if _, err := os.Stat(tc.prReview.snapshotDir + "/main.go"); err != nil {
		t.Errorf("snapshot missing main.go: %v", err)
	}
	if tc.prReview.narrative != "narrative here" {
		t.Errorf("narrative = %q, want the agent's summary", tc.prReview.narrative)
	}
}

func TestStagePRLensesMergesCapsAndAddsHallucinationDimension(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{depth: prreview.DepthQuick, aiGenerated: 0.9, snapshotDir: t.TempDir()}

	var calls atomic.Int32
	tc.Agent = &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		calls.Add(1)
		// Priority 0.5, below the hallucination-check dimension's fixed
		// priority of 1 (dimension.go), so the cap's cut is deterministic:
		// the hallucination dimension always survives, one lens dimension
		// always does not.
		return captureResult("propose_dimensions", map[string]any{
			"dimensions": []map[string]any{
				{"name": stage, "prompt": "p", "target_files": []string{"a.go"}, "priority": 0.5},
			},
		}), nil
	}}

	if err := stagePRLenses().Run(context.Background(), tc); err != nil {
		t.Fatalf("lenses: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("agent calls = %d, want 3 (one per lens)", calls.Load())
	}
	// quick depth caps at 3 dimensions total; three lenses each proposed one
	// distinctly named dimension, plus the hallucination-check dimension a
	// 0.9 AI-generated score adds as a fourth candidate -- capped to 3, not
	// 3 lens dimensions plus the hallucination dimension on top.
	if len(tc.prReview.dimensions) != prreview.MaxDimensionsFor(prreview.DepthQuick) {
		t.Fatalf("dimensions = %d, want the quick cap of %d", len(tc.prReview.dimensions), prreview.MaxDimensionsFor(prreview.DepthQuick))
	}
	foundHallucination := false
	for _, d := range tc.prReview.dimensions {
		if d.Name == "hallucination-check" {
			foundHallucination = true
		}
	}
	if !foundHallucination {
		t.Error("hallucination-check dimension was cut even though its priority of 1 outranks every lens dimension's 0.5")
	}
}

func TestStagePRReviewBoundsConcurrencyAndDistinguishesTimeoutFromCleanEmpty(t *testing.T) {
	tc := baseTaskContext(t)
	dims := make([]prreview.Dimension, 10)
	for i := range dims {
		dims[i] = prreview.Dimension{Name: fmt.Sprintf("dim-%d", i), Prompt: "p", TargetFiles: []string{"a.go"}}
	}
	tc.prReview = &prReviewState{dimensions: dims, snapshotDir: t.TempDir()}

	runner := &concurrentAgentRunner{byStage: func(stage string) (agentexec.Result, error) {
		if stage == "reviewer-dim-0" {
			// Simulate a reviewer that exhausted its turn cap before its
			// terminal tool call: no Go error, but not agentexec.StatusPassed.
			return agentexec.Result{Status: agentexec.StatusParked, StopReason: "budget_exhausted"}, nil
		}
		return captureResult("report_findings", map[string]any{"findings": []any{}}), nil
	}}
	tc.Agent = runner
	store := &threadSafeStore{}
	tc.Store = store
	tc.StepID = 1 // non-zero, so startChildStep/finishChildStep actually record

	if err := stagePRReview().Run(context.Background(), tc); err != nil {
		t.Fatalf("review: %v", err)
	}
	if runner.peak > prReviewConcurrency {
		t.Errorf("peak concurrent reviewer calls = %d, want at most %d", runner.peak, prReviewConcurrency)
	}
	if len(tc.prReview.findings) != 0 {
		t.Errorf("findings = %d, want 0 (every reviewer reported none)", len(tc.prReview.findings))
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	var timedOut, cleanEmpty int
	for _, f := range store.finishes {
		switch f.To {
		case taskstate.StepFailed:
			timedOut++
		case taskstate.StepSucceeded:
			cleanEmpty++
		default:
			t.Errorf("unexpected finish status %q", f.To)
		}
	}
	if timedOut != 1 {
		t.Errorf("StepFailed finishes = %d, want exactly 1 (the turn-cap-exhausted reviewer)", timedOut)
	}
	if cleanEmpty != 9 {
		t.Errorf("clean StepSucceeded finishes = %d, want 9", cleanEmpty)
	}
}

func TestStagePRSynthesisScoresAndCapsFindings(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		aiGenerated: 0.9,
		blastRadius: make([]string, 12),
		findings: []prreview.Finding{
			{File: "a.go", LineStart: 1, Severity: prreview.SeverityCritical, Title: "t", Body: "b", Evidence: "e", Confidence: 0.9},
		},
	}
	if err := stagePRSynthesis().Run(context.Background(), tc); err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	if len(tc.prReview.scored) != 1 {
		t.Fatalf("scored = %d, want 1", len(tc.prReview.scored))
	}
	// blast radius over 10 files and a likely machine-written PR both apply
	// their multiplier (scoring.go), so the score exceeds the base severity
	// weight x confidence (1.0 x 0.9 = 0.9).
	if tc.prReview.scored[0].Score <= 0.9 {
		t.Errorf("score = %v, want more than the unmultiplied 0.9 (blast radius + AI-generated multipliers should have applied)", tc.prReview.scored[0].Score)
	}
}

func TestStagePROutputPolishesPostsAndCompletes(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		headSHA: "deadbeef",
		scored: []prreview.ScoredFinding{
			{File: "a.go", LineStart: 3, Title: "t1", Body: "original 1"},
			{File: "b.go", LineStart: 5, Title: "t2", Body: "original 2"},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return captureResult("polish", map[string]any{"body": "tightened"}), nil
	}}
	forge := &fakeForger{}
	tc.Forge = forge

	if err := stagePROutput().Run(context.Background(), tc); err != nil {
		t.Fatalf("output: %v", err)
	}
	for _, f := range tc.prReview.scored {
		if f.Body != "tightened" {
			t.Errorf("finding %q body = %q, want the polish call's tightened body", f.Title, f.Body)
		}
	}
	if len(forge.comments) != 2 {
		t.Fatalf("posted comments = %d, want 2", len(forge.comments))
	}
	if forge.reviewedHeadSHA != "deadbeef" {
		t.Errorf("posted headSHA = %q, want deadbeef", forge.reviewedHeadSHA)
	}
	if tc.Outcome.Status != StatusCompleted {
		t.Errorf("outcome status = %q, want %q", tc.Outcome.Status, StatusCompleted)
	}
}

func TestStagePROutputKeepsOriginalBodyWhenPolishFails(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{scored: []prreview.ScoredFinding{{File: "a.go", LineStart: 1, Body: "original"}}}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return agentexec.Result{}, fmt.Errorf("model unavailable")
	}}
	tc.Forge = &fakeForger{}

	if err := stagePROutput().Run(context.Background(), tc); err != nil {
		t.Fatalf("output: %v", err)
	}
	if tc.prReview.scored[0].Body != "original" {
		t.Errorf("body = %q, want the original body kept on polish failure", tc.prReview.scored[0].Body)
	}
}

func TestBudgetExhaustedSkipsLensesAndRecordsIt(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		depth: prreview.DepthQuick, snapshotDir: t.TempDir(), runStart: time.Now(),
	}
	// Spend past every phase's cumulative token allotment up front, so
	// lenses (the first budget-tracked phase) is already exhausted before
	// its own work would start.
	tc.prReview.tokensSpent.Store(int64(prReviewTotalBudget.MaxTokens))
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		t.Fatal("no lens call should run once the budget is already spent")
		return agentexec.Result{}, nil
	}}

	if err := stagePRLenses().Run(context.Background(), tc); err != nil {
		t.Fatalf("lenses: %v", err)
	}
	if len(tc.prReview.dimensions) != 0 {
		t.Errorf("dimensions = %d, want 0 (lenses skipped, not run)", len(tc.prReview.dimensions))
	}
	if len(tc.prReview.skippedPhases) != 1 || tc.prReview.skippedPhases[0] != "lenses" {
		t.Errorf("skippedPhases = %v, want [\"lenses\"]", tc.prReview.skippedPhases)
	}
}

func TestBudgetNotExhaustedRunsLensesNormally(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{depth: prreview.DepthQuick, snapshotDir: t.TempDir(), runStart: time.Now()}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return captureResult("propose_dimensions", map[string]any{
			"dimensions": []map[string]any{{"name": "d", "prompt": "p", "target_files": []string{"a.go"}, "priority": 1.0}},
		}), nil
	}}

	if err := stagePRLenses().Run(context.Background(), tc); err != nil {
		t.Fatalf("lenses: %v", err)
	}
	if len(tc.prReview.skippedPhases) != 0 {
		t.Errorf("skippedPhases = %v, want none: the budget was not spent", tc.prReview.skippedPhases)
	}
	if len(tc.prReview.dimensions) == 0 {
		t.Error("dimensions = 0, want at least the lens's own proposal: lenses should have run")
	}
}

func TestStagePROutputReportsSkippedPhasesAndReviewEvent(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		headSHA:       "deadbeef",
		skippedPhases: []string{"verification", "coverage-consistency"},
		scored: []prreview.ScoredFinding{
			{File: "a.go", LineStart: 1, Title: "t", Body: "b", Blocking: true},
		},
	}
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		return captureResult("polish", map[string]any{"body": "tightened"}), nil
	}}
	tc.Forge = &fakeForger{}

	if err := stagePROutput().Run(context.Background(), tc); err != nil {
		t.Fatalf("output: %v", err)
	}
	if !strings.Contains(tc.Outcome.Detail, "REQUEST_CHANGES") {
		t.Errorf("outcome detail = %q, want it to name REQUEST_CHANGES (a blocking finding survived)", tc.Outcome.Detail)
	}
	if !strings.Contains(tc.Outcome.Detail, "verification") || !strings.Contains(tc.Outcome.Detail, "coverage-consistency") {
		t.Errorf("outcome detail = %q, want it to name every skipped phase", tc.Outcome.Detail)
	}
}

func TestStagePROutputSkipsPolishWhenBudgetExhausted(t *testing.T) {
	tc := baseTaskContext(t)
	tc.prReview = &prReviewState{
		runStart: time.Now(),
		scored:   []prreview.ScoredFinding{{File: "a.go", LineStart: 1, Title: "t", Body: "original"}},
	}
	tc.prReview.tokensSpent.Store(int64(prReviewTotalBudget.MaxTokens))
	tc.Agent = &concurrentAgentRunner{byStage: func(string) (agentexec.Result, error) {
		t.Fatal("no polish call should run once the budget is already spent")
		return agentexec.Result{}, nil
	}}
	tc.Forge = &fakeForger{}

	if err := stagePROutput().Run(context.Background(), tc); err != nil {
		t.Fatalf("output: %v", err)
	}
	if tc.prReview.scored[0].Body != "original" {
		t.Errorf("body = %q, want the original (unpolished) body", tc.prReview.scored[0].Body)
	}
	if !slices.Contains(tc.prReview.skippedPhases, "output") {
		t.Errorf("skippedPhases = %v, want it to include \"output\"", tc.prReview.skippedPhases)
	}
}

// fakeForger records CreateReviewComments calls; every other Forger method
// panics if called, since no pr-review stage should reach them.
type fakeForger struct {
	comments        []ReviewComment
	reviewedHeadSHA string
}

func (f *fakeForger) CloseIssue(context.Context, string, string, int, string) error { panic("unused") }

func (f *fakeForger) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	panic("unused")
}

func (f *fakeForger) LinkBranch(context.Context, string, string, int, string) error { panic("unused") }

func (f *fakeForger) CreateReviewComments(
	_ context.Context, _, _ string, _ int, reviewedHeadSHA string, comments []ReviewComment,
) error {
	f.comments = comments
	f.reviewedHeadSHA = reviewedHeadSHA
	return nil
}

func (f *fakeForger) Comment(context.Context, string, string, int, string) (int64, error) {
	panic("unused")
}

func (f *fakeForger) ReplyToReview(context.Context, string, string, int, int64, string) error {
	panic("unused")
}

// smallDiff is a one-line, one-file diff: under the 100-line quick-depth
// ceiling, so intake classifies it as quick.
const smallDiff = `diff --git a/main.go b/main.go
index e69de29..4b825dc 100644
--- a/main.go
+++ b/main.go
@@ -0,0 +1 @@
+package main
`
