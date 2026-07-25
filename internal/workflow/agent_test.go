package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
)

type fakeAgentRunner struct {
	workspace string
	request   agentexec.Request
	result    agentexec.Result
	err       error
}

type agentRunnerFunc func(context.Context, string, agentexec.Request) (agentexec.Result, error)

func (f agentRunnerFunc) Run(ctx context.Context, workspace string, req agentexec.Request) (agentexec.Result, error) {
	return f(ctx, workspace, req)
}

func (r *fakeAgentRunner) Run(_ context.Context, workspace string, req agentexec.Request) (agentexec.Result, error) {
	r.workspace = workspace
	r.request = req
	if r.result.Version == 0 {
		r.result.Version = agentexec.ProtocolVersion
	}
	r.result.TaskID = req.TaskID
	r.result.Attempt = req.Attempt
	r.result.Stage = req.Stage
	return r.result, r.err
}

func TestAgentStageBuildsExecutionRequestAndAppliesResult(t *testing.T) {
	runner := &fakeAgentRunner{result: agentexec.Result{
		Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
		Summary: "finished", TokensUsed: 23, Iterations: 4,
	}}
	resultCalled := false
	stage := AgentStage{
		Name: "plan", Role: "planner", ReadOnly: true, MaxSteps: 7,
		Mission: func(*TaskContext) string { return "inspect the repository" },
		OnResult: func(_ *TaskContext, result agentexec.Result) error {
			resultCalled = result.Summary == "finished"
			return nil
		},
	}.Stage()
	task := &store.Task{ID: 9, Attempt: 2}
	tc := &TaskContext{
		Task: task, Agent: runner, Dir: "/tmp/workspace", Log: slog.New(slog.DiscardHandler),
		Cfg: config.Config{
			Models:  map[string]string{"builder": "provider/fallback"},
			Budgets: config.Budgets{MaxSteps: 50, MaxTokens: 5000},
		},
		Repo: config.Repo{Protect: []string{"_templ.go"}, Preflight: [][]string{{"go", "version"}}},
	}
	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if runner.workspace != tc.Dir {
		t.Fatalf("workspace = %q, want %q", runner.workspace, tc.Dir)
	}
	wantPreflight := []agentexec.Command{{Name: "go", Argv: []string{"go", "version"}}}
	if runner.request.Version != agentexec.ProtocolVersion || runner.request.TaskID != 9 || runner.request.Attempt != 2 ||
		runner.request.Model != "provider/fallback" || runner.request.Budget.MaxSteps != 7 || !runner.request.ReadOnly ||
		!reflect.DeepEqual(runner.request.Preflight, wantPreflight) {
		t.Fatalf("unexpected request: %#v", runner.request)
	}
	if len(runner.request.Protection.Suffixes) != 0 {
		t.Fatalf("read-only request has protection rules: %#v", runner.request.Protection)
	}
	if !resultCalled || task.TokensUsed != 23 || task.Iterations != 4 {
		t.Fatalf("result not applied: called=%v task=%#v", resultCalled, task)
	}
}

func TestAgentStagePersistsReturnedNotes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "archie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	ctx := context.Background()
	if _, err := st.EnqueueIssue(ctx, "owner", "repo", 1, "title", "body", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeAgentRunner{result: agentexec.Result{
		Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
		AppendedNotes: []string{"checked with go test"},
	}}
	stage := AgentStage{
		Name: "build", Role: "builder", Mission: func(*TaskContext) string { return "build" },
	}.Stage()
	tc := &TaskContext{
		Task: task, Store: st, Agent: runner, Log: slog.New(slog.DiscardHandler),
		Cfg: config.Config{Models: map[string]string{"builder": "provider/model"}},
	}
	if err := stage.Run(ctx, tc); err != nil {
		t.Fatal(err)
	}
	if task.Notes != "- checked with go test\n" {
		t.Fatalf("task notes = %q", task.Notes)
	}
}

func TestAgentStageDiscardsUnstampedErrorResult(t *testing.T) {
	task := &store.Task{ID: 1, Attempt: 1, Notes: "existing\n"}
	runner := agentRunnerFunc(func(context.Context, string, agentexec.Request) (agentexec.Result, error) {
		return agentexec.Result{AppendedNotes: []string{"untrusted"}}, errors.New("worker exited")
	})
	stage := AgentStage{
		Name: "build", Role: "builder", Mission: func(*TaskContext) string { return "build" },
	}.Stage()
	err := stage.Run(context.Background(), &TaskContext{
		Task: task, Agent: runner, Log: slog.New(slog.DiscardHandler),
		Cfg: config.Config{Models: map[string]string{"builder": "provider/model"}},
	})
	if err == nil {
		t.Fatal("stage accepted an unstamped error result")
	}
	if task.Notes != "existing\n" {
		t.Fatalf("unstamped error result mutated notes: %q", task.Notes)
	}
}

func TestFeasibilityDecisionCrossesAsCapturedData(t *testing.T) {
	runner := &fakeAgentRunner{result: agentexec.Result{
		Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
		Captures: map[string][]json.RawMessage{"decide": {json.RawMessage(`{"fit":true,"reasons":"aligned"}`)}},
	}}
	tc := &TaskContext{
		Task: &store.Task{ID: 1}, Agent: runner, Log: slog.New(slog.DiscardHandler),
		Cfg:  config.Config{Models: map[string]string{"planner": "provider/model"}},
		Repo: config.Repo{Owner: "owner", Name: "repo"},
	}
	if err := Feasibility().Stages[1].Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if tc.decision == nil || !tc.decision.Fit || tc.decision.Reasons != "aligned" {
		t.Fatalf("decision = %#v", tc.decision)
	}
}

func TestFeasibilityRejectsNullFitValue(t *testing.T) {
	runner := &fakeAgentRunner{result: agentexec.Result{
		Status:   agentexec.StatusPassed,
		Captures: map[string][]json.RawMessage{"decide": {json.RawMessage(`{"fit":null,"reasons":"not valid"}`)}},
	}}
	tc := &TaskContext{
		Task: &store.Task{ID: 1}, Agent: runner, Log: slog.New(slog.DiscardHandler),
		Cfg:  config.Config{Models: map[string]string{"planner": "provider/model"}},
		Repo: config.Repo{Owner: "owner", Name: "repo"},
	}
	if err := Feasibility().Stages[1].Run(context.Background(), tc); err == nil {
		t.Fatal("assess accepted a null fit value")
	}
	if tc.decision != nil {
		t.Fatalf("decision mutated from invalid capture: %#v", tc.decision)
	}
}

// ── regression: Gap 5  --  daemon review step ──────────────────────────

func TestReviewResultBlocksOnResultWhenRejected(t *testing.T) {
	// Gap 5: no daemon review step before human delivery.
	// PRD section 1: the daemon reviews agent responses before forwarding
	// to human channels. When ReviewResult returns an error, the stage
	// must fail and OnResult must NOT be called. When ReviewResult
	// returns nil, OnResult IS called. Currently ReviewResult exists
	// but no workflow uses it  --  the review step is never exercised.
	runner := &fakeAgentRunner{result: agentexec.Result{
		Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
		Summary: "sensitive output", TokensUsed: 1,
	}}

	// Test 1: ReviewResult rejects → OnResult must not be called.
	onResultCalled := false
	rejectStage := AgentStage{
		Name:    "plan",
		Role:    "planner",
		Mission: func(*TaskContext) string { return "test" },
		ReviewResult: func(_ *TaskContext, _ agentexec.Result) error {
			return agentexec.ErrBlocked
		},
		OnResult: func(_ *TaskContext, _ agentexec.Result) error {
			onResultCalled = true
			return nil
		},
	}.Stage()
	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Attempt: 1},
		Agent: runner, Log: slog.New(slog.DiscardHandler),
		Cfg: config.Config{Models: map[string]string{"planner": "provider/model"}},
	}
	err := rejectStage.Run(context.Background(), tc)
	if err == nil {
		t.Error("Gap 5: ReviewResult returned an error but the stage did not fail. " +
			"ReviewResult must block the stage when it rejects output.")
	}
	if onResultCalled {
		t.Error("Gap 5: ReviewResult rejected the output but OnResult was still called. " +
			"ReviewResult must gate OnResult  --  rejected output must not reach human channels.")
	}

	// Test 2: ReviewResult approves → OnResult IS called.
	onResultCalled = false
	approveStage := AgentStage{
		Name:    "plan",
		Role:    "planner",
		Mission: func(*TaskContext) string { return "test" },
		ReviewResult: func(_ *TaskContext, _ agentexec.Result) error {
			return nil // approved
		},
		OnResult: func(_ *TaskContext, _ agentexec.Result) error {
			onResultCalled = true
			return nil
		},
	}.Stage()
	if err := approveStage.Run(context.Background(), tc); err != nil {
		t.Fatalf("unexpected stage error: %v", err)
	}
	if !onResultCalled {
		t.Error("Gap 5: ReviewResult approved the output but OnResult was not called. " +
			"Approved output must flow through to human channels.")
	}
}

func TestRunLeavesInterruptedTaskForCrashRecovery(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "archie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if _, err := st.EnqueueIssue(context.Background(), "owner", "repo", 2, "title", "body", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Run(ctx, Workflow{Name: "test", Stages: []Stage{{
		Name: "agent", Run: func(ctx context.Context, _ *TaskContext) error { return ctx.Err() },
	}}}, &TaskContext{
		Task: task, Store: st, Repo: config.Repo{Owner: "owner", Name: "repo"},
		Log: slog.New(slog.DiscardHandler),
	})
	got, err := st.TaskByIssue(context.Background(), "owner", "repo", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusRunning || got.ParkReason != "" {
		t.Fatalf("interrupted task status=%q park_reason=%q, want running with no park reason", got.Status, got.ParkReason)
	}
}
