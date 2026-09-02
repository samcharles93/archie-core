package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
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
		Task:  &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
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
	if tc.Outcome.Status != store.StatusMerged {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusMerged)
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
		Task:  &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1_000_000_000_000_001, Source: store.SourceChat},
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
	if tc.Outcome.Status != store.StatusMerged {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusMerged)
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
		Task:  &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
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
	if tc.Outcome.Status != store.StatusQueued {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, store.StatusQueued)
	}
}

// TestTriageDefaultsToImplementForUnrecognizedWorkflow confirms a missing
// or unrecognized workflow choice degrades to "implement" rather than
// producing an unroutable task.
func TestTriageDefaultsToImplementForUnrecognizedWorkflow(t *testing.T) {
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{
			Version:  agentexec.ProtocolVersion,
			TaskID:   req.TaskID,
			Attempt:  req.Attempt,
			Stage:    req.Stage,
			Status:   agentexec.StatusPassed,
			Captures: map[string][]json.RawMessage{"decide": triageDecision(t, true, "not-a-real-workflow", "unsure, defaulting")},
		}, nil
	})
	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Log:   slog.New(slog.DiscardHandler),
	}

	if err := classifyStage().Run(context.Background(), tc); err != nil {
		t.Fatalf("classify stage = %v, want nil", err)
	}
	if tc.Task.Workflow != "implement" {
		t.Fatalf("Task.Workflow = %q, want %q", tc.Task.Workflow, "implement")
	}
}

// TestTriageRejectsMissingDecideCall pins that classify must not silently
// pass through without a verdict -- mirrors feasibility's assess stage.
func TestTriageRejectsMissingDecideCall(t *testing.T) {
	runner := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		return agentexec.Result{Version: agentexec.ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt, Stage: req.Stage, Status: agentexec.StatusPassed}, nil
	})
	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Attempt: 1, Owner: "o", Repo: "r", IssueNumber: 1},
		Repo:  config.Repo{Owner: "o", Name: "r"},
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
		Agent: runner,
		Log:   slog.New(slog.DiscardHandler),
	}

	if err := classifyStage().Run(context.Background(), tc); err == nil {
		t.Fatal("classify stage = nil, want an error when decide was never called")
	}
}
