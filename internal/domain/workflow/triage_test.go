package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
)

func triageDecision(t *testing.T, needsCodeChange bool, workflow, reasons string) []json.RawMessage {
	t.Helper()
	payload := map[string]any{"needs_code_change": needsCodeChange, "reasons": reasons}
	if workflow != "" {
		payload["workflow"] = workflow
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return []json.RawMessage{raw}
}

func classifyStage() Stage {
	wf := Triage()
	for _, s := range wf.Stages {
		if s.Name == "classify" {
			return s
		}
	}
	panic("triage workflow has no classify stage")
}

// TestTriageClosesWithoutCodeChange is the regression case for
// archie-core-enfj: a task the classifier judges needs no code change
// closes immediately, without ever reaching baseline/plan/build.
func TestTriageClosesWithoutCodeChange(t *testing.T) {
	f := &fakeForge{}
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version:  agentexec.ProtocolVersion,
			TaskID:   req.TaskID,
			Attempt:  req.Attempt,
			Stage:    req.Stage,
			Status:   agentexec.StatusPassed,
			Captures: map[string][]json.RawMessage{"decide": triageDecision(t, false, "", "purely conversational, nothing to build")},
		}, nil
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Forge: f,
		Log:   slog.New(slog.DiscardHandler),
	}

	if err := classifyStage().Run(context.Background(), tc); err != nil {
		t.Fatalf("classify stage = %v, want nil", err)
	}
	if f.closed != 1 {
		t.Fatalf("CloseIssue calls = %d, want 1", f.closed)
	}
	if tc.Outcome.Status != StatusCompleted {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, StatusCompleted)
	}
	if tc.Task.Workflow != "" {
		t.Fatalf("Task.Workflow = %q, want unset (no requeue on the close path)", tc.Task.Workflow)
	}
}

// TestTriageClosesChatTaskWithoutForgeCall confirms a chat-spawned task
// (synthetic issue number, no real forge issue behind it) closes via the
// Outcome alone -- never calling CloseIssue against a number that does
// not exist on the forge.
func TestTriageClosesChatTaskWithoutForgeCall(t *testing.T) {
	f := &fakeForge{}
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version:  agentexec.ProtocolVersion,
			TaskID:   req.TaskID,
			Attempt:  req.Attempt,
			Stage:    req.Stage,
			Status:   agentexec.StatusPassed,
			Captures: map[string][]json.RawMessage{"decide": triageDecision(t, false, "", "just a test")},
		}, nil
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1_000_000_000_000_001, Source: SourceChat},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Forge: f,
		Log:   slog.New(slog.DiscardHandler),
	}

	if err := classifyStage().Run(context.Background(), tc); err != nil {
		t.Fatalf("classify stage = %v, want nil", err)
	}
	if f.closed != 0 {
		t.Fatalf("CloseIssue calls = %d, want 0 for a chat-spawned (non-forge-backed) task", f.closed)
	}
	if tc.Outcome.Status != StatusCompleted {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, StatusCompleted)
	}
}

// TestTriageRequeuesUnderChosenWorkflow confirms a task the classifier
// judges needs a code change gets tc.Task.Workflow set to its choice and
// an Outcome that requeues it -- never running build itself.
func TestTriageRequeuesUnderChosenWorkflow(t *testing.T) {
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version:  agentexec.ProtocolVersion,
			TaskID:   req.TaskID,
			Attempt:  req.Attempt,
			Stage:    req.Stage,
			Status:   agentexec.StatusPassed,
			Captures: map[string][]json.RawMessage{"decide": triageDecision(t, true, "tdd", "reported bug, needs a repro test first")},
		}, nil
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Log:   slog.New(slog.DiscardHandler),
	}

	if err := classifyStage().Run(context.Background(), tc); err != nil {
		t.Fatalf("classify stage = %v, want nil", err)
	}
	if tc.Task.Workflow != "tdd" {
		t.Fatalf("Task.Workflow = %q, want %q", tc.Task.Workflow, "tdd")
	}
	if tc.Outcome.Status != StatusQueued {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, StatusQueued)
	}
}

// Defaulting an unrecognised workflow to "implement" let the classifier skip
// the decision and send an unsettled capability straight to coding, which is
// the failure the selection criteria exist to prevent. Omission is caught
// earlier, by the decide tool's conditional requirement, so the model is
// told and retries; a value that is not in the enum at all means the model
// ignored its schema, and parking is the honest outcome.
func TestTriageRefusesAnUnrecognizedWorkflow(t *testing.T) {
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version:  agentexec.ProtocolVersion,
			TaskID:   req.TaskID,
			Attempt:  req.Attempt,
			Stage:    req.Stage,
			Status:   agentexec.StatusPassed,
			Captures: map[string][]json.RawMessage{"decide": triageDecision(t, true, "not-a-real-workflow", "unsure")},
		}, nil
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Log:   slog.New(slog.DiscardHandler),
	}

	err := classifyStage().Run(context.Background(), tc)
	if err == nil {
		t.Fatal("classify stage accepted an unrecognised workflow")
	}
	if !strings.Contains(err.Error(), "not-a-real-workflow") {
		t.Fatalf("error %q does not name the rejected workflow", err)
	}
	if tc.Task.Workflow != "" {
		t.Fatalf("Task.Workflow = %q, want it left unset", tc.Task.Workflow)
	}
}

// The decide tool must demand a workflow exactly when one is needed, so an
// omission is a retryable rejection rather than a silent default, and the
// close-without-change branch is not made to invent an answer.
func TestTriageRequiresAWorkflowOnlyWhenACodeChangeIsNeeded(t *testing.T) {
	tools := triageDecideCaptureTools(nil)
	if len(tools) != 1 {
		t.Fatalf("triageDecideCaptureTools returned %d tools, want 1", len(tools))
	}
	required := tools[0].RequiredWhenTrue["needs_code_change"]
	if !slices.Contains(required, "workflow") {
		t.Fatalf("decide requires %v when needs_code_change is true, want it to include \"workflow\"", required)
	}
	if slices.Contains(tools[0].RequiredFields, "workflow") {
		t.Error("decide requires workflow unconditionally; the close-without-change branch has no workflow to give")
	}
}

// TestTriageRejectsMissingDecideCall pins that classify must not silently
// pass through without a verdict -- mirrors feasibility's assess stage.
func TestTriageRejectsMissingDecideCall(t *testing.T) {
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{Version: agentexec.ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt, Stage: req.Stage, Status: agentexec.StatusPassed}, nil
	})
	tc := &TaskContext{
		Task:  &Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Log:   slog.New(slog.DiscardHandler),
	}

	if err := classifyStage().Run(context.Background(), tc); err == nil {
		t.Fatal("classify stage = nil, want an error when decide was never called")
	}
}

// The classifier picks between three workflows, and until this was written
// neither the mission nor the decide schema said what any of them is for:
// the mission spent its guidance on needs_code_change and the schema said
// only "Which workflow fits best", with implement as the free default. Issue
// 813 (a new capability with no design doc) was classified implement on that
// basis and produced a half-wired slice, where feasibility would have
// produced the design doc this repo requires first.
//
// This asserts the criteria are stated, not how they are worded: each target
// must be named alongside the condition that selects it.
func TestTriageStatesItsWorkflowSelectionCriteria(t *testing.T) {
	stage := classifyStage()
	tc := &TaskContext{Task: &Task{Title: "t", Body: "b"}, Cfg: config.Config{}, Repo: config.Repo{Owner: "o", Name: "r"}}

	tools := triageDecideCaptureTools(tc)
	if len(tools) != 1 {
		t.Fatalf("triageDecideCaptureTools returned %d tools, want 1", len(tools))
	}
	schema := string(tools[0].Parameters)

	for _, want := range []string{"feasibility", "tdd", "implement"} {
		if !strings.Contains(schema, want) {
			t.Errorf("decide schema never names the %q workflow", want)
		}
	}

	// The criteria themselves. Each phrase is the discriminator a classifier
	// needs to tell the three apart; without them the enum is unguided.
	for _, want := range []string{"design", "reproduc"} {
		if !strings.Contains(strings.ToLower(schema), want) {
			t.Errorf("decide schema states no %q criterion for choosing a workflow", want)
		}
	}
	if stage.Name != "classify" {
		t.Fatalf("classify stage is named %q", stage.Name)
	}

	// The mission is what the model actually reads first, so the criteria
	// have to be there too, not only in the tool schema.
	mission := strings.ToLower(triageMission(tc))
	for _, want := range []string{"feasibility", "tdd", "implement", "design", "reproduce"} {
		if !strings.Contains(mission, want) {
			t.Errorf("triage mission never mentions %q", want)
		}
	}
}
