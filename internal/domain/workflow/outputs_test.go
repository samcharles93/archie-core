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
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// outputsRunYAML is the smallest workflow with a declared output: one agent
// stage under repository none.
const outputsRunYAML = "id: w\nrepository: none\noutputs:\n  verdict: {type: bool, required: true}\n  summary: {type: string}\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n"

// outputsTC returns a TaskContext wired like the runtime tests, with the
// workflow's own YAML pinned on the task row.
func outputsTC(t *testing.T, yaml string, store Store, agent agentexec.Runner) *TaskContext {
	t.Helper()
	return &TaskContext{
		Task:  &Task{ID: 3, Attempt: 1, WorkflowDefinitionYAML: yaml},
		Store: store, Trees: &fakeTrees{dir: "/scratch/3"}, Agent: agent,
		Cfg: config.Config{Models: map[string]string{"builder": "p/m"}},
		Log: slog.New(slog.DiscardHandler),
	}
}

// outputCaptureResult builds a passed agent result carrying one accepted capture
// call for the output tool named name.
func outputCaptureResult(name string, args ...string) agentexec.Result {
	captures := map[string][]json.RawMessage{}
	for _, raw := range args {
		captures[name] = append(captures[name], json.RawMessage(raw))
	}
	return agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed, Summary: "done", Captures: captures}
}

// TestRunOffersOutputsAsCaptureToolsAndAppliesThem pins the agent write path
// (docs/prds/workflow-call-outputs.md, "How a run writes one"): each declared
// output is a capture tool named after it with a {"value": ...} argument, and
// an accepted call enters the run's output set, which the finish write
// persists into the row before the outcome transition.
func TestRunOffersOutputsAsCaptureToolsAndAppliesThem(t *testing.T) {
	wf := compileCaller(t, outputsRunYAML)
	runner := &fakeAgentRunner{result: outputCaptureResult("verdict", `{"value": true}`)}
	store := &recordingStore{}
	tc := outputsTC(t, outputsRunYAML, store, runner)
	Run(context.Background(), wf, tc)

	var offered bool
	for _, tool := range runner.request.CaptureTools {
		if tool.Name == "verdict" {
			offered = true
			if tool.MaxCalls != 1 {
				t.Errorf("verdict tool MaxCalls = %d, want 1: each output is written once per attempt", tool.MaxCalls)
			}
			if !slicesContains(tool.RequiredFields, "value") {
				t.Errorf("verdict tool RequiredFields = %v, want [value]", tool.RequiredFields)
			}
			if !strings.Contains(string(tool.Parameters), `"value"`) {
				t.Errorf("verdict tool parameters = %s, want a value argument", tool.Parameters)
			}
		}
	}
	if !offered {
		t.Fatalf("CaptureTools = %+v, want a tool named for the declared output", runner.request.CaptureTools)
	}
	if got := tc.Task.Outputs["verdict"]; got != true {
		t.Fatalf("run outputs = %+v, want {verdict: true} with bool restored", tc.Task.Outputs)
	}
	// The finish write replaces the row's set wholesale before the outcome
	// transition carries the value to any caller.
	if len(store.updates) == 0 {
		t.Fatal("the run never persisted its row")
	}
	lastUpdate := store.updates[len(store.updates)-1]
	if lastUpdate.Outputs["verdict"] != true {
		t.Fatalf("row outputs = %+v, want the written verdict", lastUpdate.Outputs)
	}
	if seq := updatesBeforeTransition(store.sequence, "completed"); seq == -1 {
		t.Fatalf("sequence = %v, want an update before the completed transition", store.sequence)
	} else if store.updates[seq].Outputs["verdict"] != true {
		t.Fatalf("update %d outputs = %+v, want the written verdict persisted before the transition", seq, store.updates[seq].Outputs)
	}
}

// TestWrittenOutputIsNotOfferedToALaterStage pins the once-per-attempt rule:
// a stage is not offered an output the run has written, so a later stage
// cannot overwrite a value.
func TestWrittenOutputIsNotOfferedToALaterStage(t *testing.T) {
	yaml := string(append([]byte("id: w\nrepository: none\noutputs:\n  verdict: {type: bool}\nsteps:\n"), []byte("  - type: agent.run\n    settings:\n      mission: first\n  - type: agent.run\n    settings:\n      mission: second\n")...))
	wf := compileCaller(t, yaml)
	var offered []bool
	agent := agentRunnerFunc(func(_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
		for _, tool := range req.CaptureTools {
			if tool.Name == "verdict" {
				offered = append(offered, true)
			}
		}
		return agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed, Summary: "ok"}, nil
	})
	tc := outputsTC(t, yaml, &recordingStore{}, agent)
	Run(context.Background(), wf, tc)
	if len(offered) != 1 {
		t.Fatalf("verdict tool offered %d times (%v), want exactly once across the run's stages", len(offered), offered)
	}
}

// TestCapturedOutputIsReValidated pins the run-time type check: a captured
// value is re-validated against the declared type when the run reads it
// back, and a null counts as not written.
func TestCapturedOutputIsReValidated(t *testing.T) {
	t.Run("mistyped capture parks naming the output", func(t *testing.T) {
		wf := compileCaller(t, outputsRunYAML)
		store := &recordingStore{}
		agent := &fakeAgentRunner{result: outputCaptureResult("verdict", `{"value": "yes"}`)}
		tc := outputsTC(t, outputsRunYAML, store, agent)
		Run(context.Background(), wf, tc)
		if tc.Outcome.Status == StatusCompleted && !strings.Contains(tc.Task.ParkReason, `output "verdict" is string, want bool`) {
			t.Fatalf("park reason %q, want the mistyped output named; outcome %+v", tc.Task.ParkReason, tc.Outcome)
		}
	})
	t.Run("null capture counts as not written", func(t *testing.T) {
		yaml := "id: w\nrepository: none\noutputs:\n  summary: {type: string}\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n"
		wf := compileCaller(t, yaml)
		store := &recordingStore{}
		agent := &fakeAgentRunner{result: outputCaptureResult("summary", `{"value": null}`)}
		tc := outputsTC(t, yaml, store, agent)
		Run(context.Background(), wf, tc)
		if tc.Outcome.Status != StatusCompleted {
			t.Fatalf("outcome = %+v, want completed (the optional output stayed absent)", tc.Outcome)
		}
		if _, written := tc.Task.Outputs["summary"]; written {
			t.Fatalf("run outputs = %+v, want the null write to count as not written", tc.Task.Outputs)
		}
	})
}

// TestRequiredOutputNeverWrittenParks pins the promise rule: a run whose
// required output is never written parks naming the output, reaching no
// successful terminal state (docs/prds/workflow-call-outputs.md, "Failure
// rules").
func TestRequiredOutputNeverWrittenParks(t *testing.T) {
	wf := compileCaller(t, outputsRunYAML)
	store := &recordingStore{}
	agent := &fakeAgentRunner{result: agentexec.Result{Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed, Summary: "done"}}
	tc := outputsTC(t, outputsRunYAML, store, agent)
	Run(context.Background(), wf, tc)
	if !strings.Contains(tc.Task.ParkReason, `output "verdict" is required`) {
		t.Fatalf("park reason %q, want the missing required output named", tc.Task.ParkReason)
	}
	if len(store.transitions) == 0 || store.transitions[len(store.transitions)-1].to != StatusParked {
		t.Fatalf("transitions = %+v, want the run parked rather than reaching a terminal state", store.transitions)
	}
}

// TestGoStageOutputRules pins the deterministic Go path: a stage sets the
// same values through WriteOutput, which refuses an undeclared key, a
// mistyped value and a second write; a key a stage bypasses it with parks at
// the finish check, identically to a captured one.
func TestGoStageOutputRules(t *testing.T) {
	iface := task.WorkflowInterface{Outputs: map[string]task.OutputSpec{
		"verdict": {Type: "bool"},
		"note":    {Type: "string"},
	}}
	tc := &TaskContext{Task: &Task{ID: 1, WorkflowDefinitionYAML: "id: w\noutputs:\n  verdict: {type: bool}\n"}}
	tc.runInterface, tc.runInterfaceSet = iface, true

	if err := tc.WriteOutput("verdict", true); err != nil {
		t.Fatalf("WriteOutput(verdict) = %v, want nil", err)
	}
	if got := tc.Task.Outputs["verdict"]; got != true {
		t.Fatalf("outputs = %+v, want the written value", got)
	}
	if err := tc.WriteOutput("verdict", false); err == nil || !strings.Contains(err.Error(), "verdict") {
		t.Fatalf("second WriteOutput(verdict) = %v, want a refusal naming the output", err)
	}
	if err := tc.WriteOutput("bogus", 7); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("WriteOutput(bogus) = %v, want an undeclared-output refusal", err)
	}
	if err := tc.WriteOutput("note", json.Number("3")); err == nil || !strings.Contains(err.Error(), `is number, want string`) {
		t.Fatalf("WriteOutput(note, number) = %v, want a type refusal", err)
	}

	// A value set around WriteOutput is judged at finish, so a bypass cannot
	// smuggle an undeclared key into the row.
	wf := Workflow{Name: "w", Stages: []Stage{{Name: "set", Run: func(_ context.Context, tc *TaskContext) error {
		tc.Task.Outputs = map[string]any{"bogus": 7}
		tc.Outcome = Outcome{Status: StatusCompleted, Detail: "done"}
		return nil
	}}}, Interface: iface}
	store := &recordingStore{}
	tc = &TaskContext{Task: &Task{ID: 9, WorkflowDefinitionYAML: "id: w\n"}, Store: store, Trees: &fakeTrees{}, Log: slog.New(slog.DiscardHandler)}
	Run(context.Background(), wf, tc)
	if !strings.Contains(tc.Task.ParkReason, `"bogus" is not declared`) {
		t.Fatalf("park reason %q, want the undeclared key named; outcome %+v", tc.Task.ParkReason, tc.Outcome)
	}
}

// TestStageCaptureToolSharingOutputNameRefused pins the ambiguity refusal: a
// stage's own capture tool may not share a declared output's name, because
// the value would otherwise be ambiguous
// (docs/prds/workflow-call-outputs.md, "Failure rules").
func TestStageCaptureToolSharingOutputNameRefused(t *testing.T) {
	stage := AgentStage{
		Name:    "decide",
		Role:    "builder",
		Mission: func(*TaskContext) string { return "m" },
		CaptureTools: func(*TaskContext) []agentexec.CaptureTool {
			return []agentexec.CaptureTool{{Name: "verdict", MaxCalls: 1}}
		},
	}.Stage()
	tc := &TaskContext{
		Task:  &Task{ID: 3, WorkflowDefinitionYAML: outputsRunYAML},
		Agent: &fakeAgentRunner{result: outputCaptureResult("verdict", `{"value": true}`)},
		Cfg:   config.Config{Models: map[string]string{"builder": "p/m"}},
		Log:   slog.New(slog.DiscardHandler),
	}
	err := stage.Run(context.Background(), tc)
	if err == nil || !strings.Contains(err.Error(), "verdict") {
		t.Fatalf("stage error = %v, want a refusal naming the shared tool name", err)
	}
}

func slicesContains(list []string, want string) bool {
	return slices.Contains(list, want)
}

// updatesBeforeTransition returns the index of the last update recorded
// before the transition to to_ was asked for, or -1 when there is none.
func updatesBeforeTransition(sequence []string, to string) int {
	last := -1
	for i, entry := range sequence {
		if entry == "update" {
			last = i
			continue
		}
		if entry == "transition:"+to {
			return last
		}
	}
	return -1
}

// TestOutputCaptureToolSchemaOmitsUnconstrainedType pins the "any" output's
// tool schema: no valid JSON Schema type exists for an unconstrained value, so
// the property carries none (a null type is not a valid schema, and would
// misguide a strict adapter).
func TestOutputCaptureToolSchemaOmitsUnconstrainedType(t *testing.T) {
	for _, test := range []struct {
		spec task.OutputSpec
		want string
	}{
		{spec: task.OutputSpec{Type: "bool"}, want: `"type":"boolean"`},
		{spec: task.OutputSpec{Type: "any"}, want: ""},
	} {
		tool := outputCaptureTool("out", test.spec)
		if strings.Contains(string(tool.Parameters), `"type":null`) {
			t.Fatalf("output type %q parameters = %s, want no null schema type", test.spec.Type, tool.Parameters)
		}
		if test.want != "" && !strings.Contains(string(tool.Parameters), test.want) {
			t.Fatalf("output type %q parameters = %s, want containing %s", test.spec.Type, tool.Parameters, test.want)
		}
	}
}
