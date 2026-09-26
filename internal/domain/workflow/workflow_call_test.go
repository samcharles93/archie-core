package workflow

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

func callRegistry() StepRegistry {
	registry := BuiltinStepRegistry()
	registry[AgentRunStepName] = newAgentRunStage
	registry[WorkflowCallStepName] = newWorkflowCallStage
	return registry
}

// fakeCaller records StartCall/CallStatus and answers the poll with a
// scripted sequence of statuses.
type fakeCaller struct {
	starts    []startCall
	statuses  map[int64][]statusAt
	statusErr error
	startErr  error
}

type startCall struct {
	callerID int64
	workflow string
	inputs   map[string]any
}

type statusAt struct {
	status string
	detail string
}

func (f *fakeCaller) StartCall(_ context.Context, callerID int64, workflow string, inputs map[string]any) (*task.Task, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.starts = append(f.starts, startCall{callerID: callerID, workflow: workflow, inputs: inputs})
	callee := &task.Task{ID: int64(len(f.starts) + 100), Workflow: workflow, CallParentTaskID: callerID, CallDepth: 1, Status: StatusQueued}
	return callee, nil
}

func (f *fakeCaller) CallStatus(_ context.Context, callerID, callID int64) (string, string, error) {
	if f.statusErr != nil {
		return "", "", f.statusErr
	}
	seq := f.statuses[callID]
	if len(seq) == 0 {
		return "", "", nil
	}
	at := seq[0]
	if len(seq) > 1 {
		f.statuses[callID] = seq[1:]
	}
	return at.status, at.detail, nil
}

func callerYAML(callee string, extra ...string) string {
	body := "id: caller\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: " + callee + "\n"
	for _, line := range extra {
		body += line + "\n"
	}
	return body
}

// ---- Save-time: collection validation ----

func TestValidateCollectionWorkflowCalls(t *testing.T) {
	for _, test := range []struct {
		name       string
		collection WorkflowDefinitionCollection
		want       string
	}{
		{
			name: "callee is not defined",
			collection: collectionOf(
				callerYAML("missing"),
			),
			want: `calls "missing", which is not defined`,
		},
		{
			name: "direct self-call",
			collection: collectionOf(
				"id: w\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: w\n",
			),
			want: `calls itself through w -> w`,
		},
		{
			name: "indirect cycle",
			collection: collectionOf(
				"id: a\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: b\n",
				"id: b\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: a\n",
			),
			want: "calls itself through a -> b -> a",
		},
		{
			name: "reference to an undeclared caller input",
			collection: collectionOf(
				callerYAML("callee", "      inputs: {src_ip: \"inputs.missing\"}"),
				"id: callee\ninputs:\n  src_ip: {type: string}\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n",
			),
			want: `input "src_ip" references "inputs.missing", which the calling workflow does not declare`,
		},
		{
			name: "callee input type mismatch",
			collection: collectionOf(
				"id: caller\ninputs:\n  src_ip: {type: string}\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      inputs: {src_ip: \"inputs.src_ip\"}\n",
				"id: callee\ninputs:\n  src_ip: {type: number}\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n",
			),
			want: `input "src_ip" is string, want number`,
		},
		{
			name: "required callee input is not assigned",
			collection: collectionOf(
				"id: caller\ninputs:\n  src_ip: {type: string}\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      inputs: {severity: \"inputs.src_ip\"}\n",
				"id: callee\ninputs:\n  src_ip: {type: string, required: true}\n  severity: {type: string}\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n",
			),
			want: `input "src_ip" is required by "callee"`,
		},
		{
			name: "undeclared callee input",
			collection: collectionOf(
				"id: caller\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      inputs: {extra: 7}\n",
				"id: callee\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n",
			),
			want: `input "extra" is not declared by "callee"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDefinitionCollection(test.collection, callRegistry())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateDefinitionCollection() error = %v, want containing %q", err, test.want)
			}
		})
	}

	valid := collectionOf(
		"id: caller\nrepository: none\ninputs:\n  src_ip: {type: string, required: true}\n  n: {type: number}\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      inputs: {src_ip: \"inputs.src_ip\", n: 3}\n",
		"id: callee\ninputs:\n  src_ip: {type: string, required: true}\n  n: {type: number}\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n",
	)
	if err := ValidateDefinitionCollection(valid, callRegistry()); err != nil {
		t.Fatalf("ValidateDefinitionCollection(valid) = %v, want nil", err)
	}

	// A chain of distinct workflows is not a cycle.
	chain := collectionOf(
		"id: a\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: b\n",
		"id: b\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: c\n",
		"id: c\nsteps:\n  - type: agent.run\n    settings:\n      mission: m\n",
	)
	if err := ValidateDefinitionCollection(chain, callRegistry()); err != nil {
		t.Fatalf("ValidateDefinitionCollection(chain) = %v, want nil", err)
	}
}

func collectionOf(yamls ...string) WorkflowDefinitionCollection {
	c := WorkflowDefinitionCollection{}
	for _, y := range yamls {
		c.Definitions = append(c.Definitions, WorkflowDefinitionEntry{ID: yamlID(y), YAML: y})
	}
	return c
}

func yamlID(y string) string {
	for line := range strings.SplitSeq(y, "\n") {
		if after, ok := strings.CutPrefix(line, "id: "); ok {
			return after
		}
	}
	return ""
}

// ---- Runtime: the stage ----

func compileCaller(t *testing.T, yaml string) Workflow {
	t.Helper()
	wf, err := ParseAndCompile(yaml, callRegistry())
	if err != nil {
		t.Fatalf("ParseAndCompile: %v", err)
	}
	return wf
}

func TestWorkflowCallStageRefusesWithoutCapability(t *testing.T) {
	wf := compileCaller(t, "id: caller\nrepository: none\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n")
	store := &recordingStore{}
	tc := &TaskContext{Task: &Task{ID: 3, Attempt: 1}, Store: store, Trees: &fakeTrees{}, Log: slog.New(slog.DiscardHandler)}
	Run(context.Background(), wf, tc)
	if !strings.Contains(tc.Task.ParkReason, "callee capability") {
		t.Fatalf("park reason %q; want a park naming the missing capability", tc.Task.ParkReason)
	}
	if len(store.ofKind(events.KindWorkflowCallStarted)) != 0 {
		t.Error("a refused call must not record a started event")
	}
}

func TestWorkflowCallStageDepthLimit(t *testing.T) {
	wf := compileCaller(t, "id: caller\nrepository: none\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n")
	caller := &fakeCaller{}
	store := &recordingStore{}
	deep := &Task{ID: 3, Attempt: 1, CallDepth: MaxCallDepth}
	tc := &TaskContext{Task: deep, Calls: caller, Store: store, Trees: &fakeTrees{}, Log: slog.New(slog.DiscardHandler)}
	Run(context.Background(), wf, tc)
	if len(caller.starts) != 0 {
		t.Fatalf("StartCall ran %d times at the depth limit, want none", len(caller.starts))
	}
	if !strings.Contains(deep.ParkReason, "depth") {
		t.Fatalf("park reason %q; want a park naming the depth limit", deep.ParkReason)
	}
}

func TestWorkflowCallWaitFalseStartsAndContinues(t *testing.T) {
	wf := compileCaller(t, "id: caller\nrepository: none\ninputs:\n  src_ip: {type: string}\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      inputs: {src_ip: \"inputs.src_ip\"}\n      wait: false\n  - type: agent.run\n    settings:\n      mission: report\n")
	caller := &fakeCaller{}
	store := &recordingStore{}
	tc := &TaskContext{
		Task:  &Task{ID: 3, Attempt: 1, Inputs: map[string]any{"src_ip": "10.0.0.9"}},
		Calls: caller, Store: store, Trees: &fakeTrees{dir: "/scratch/3"}, Log: slog.New(slog.DiscardHandler),
		Agent: &fakeAgentRunner{result: agentexec.Result{Status: agentexec.StatusPassed, Summary: "reported"}},
		Cfg:   config.Config{Models: map[string]string{"builder": "p/m"}},
	}
	Run(context.Background(), wf, tc)
	if len(caller.starts) != 1 {
		t.Fatalf("StartCall ran %d times, want exactly one", len(caller.starts))
	}
	start := caller.starts[0]
	if start.callerID != 3 || start.workflow != "callee" {
		t.Errorf("start = %+v, want caller 3 calling callee", start)
	}
	if got := start.inputs["src_ip"]; got != "10.0.0.9" {
		t.Errorf("input src_ip = %v, want the caller's resolved input", got)
	}
	if tc.Outcome.Status != StatusCompleted {
		t.Errorf("outcome = %+v, want the caller to continue and complete", tc.Outcome)
	}
	started := store.ofKind(events.KindWorkflowCallStarted)
	if len(started) != 1 || started[0].Data["callee_task_id"] != int64(101) {
		t.Errorf("started events = %+v, want one carrying callee task 101", started)
	}
}

func TestWorkflowCallWaitTrueWaitsForTerminalCallee(t *testing.T) {
	wf := compileCaller(t, "id: caller\nrepository: none\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      wait: true\n  - type: agent.run\n    settings:\n      mission: report\n")
	caller := &fakeCaller{statuses: map[int64][]statusAt{
		101: {{StatusQueued, ""}, {StatusRunning, ""}, {StatusCompleted, "contained 10.0.0.9"}},
	}}
	store := &recordingStore{}
	tc := &TaskContext{
		Task: &Task{ID: 3, Attempt: 1}, Calls: caller, Store: store,
		Trees: &fakeTrees{dir: "/scratch/3"}, Log: slog.New(slog.DiscardHandler),
		Agent: &fakeAgentRunner{result: agentexec.Result{Status: agentexec.StatusPassed, Summary: "reported"}},
		Cfg:   config.Config{Models: map[string]string{"builder": "p/m"}},
	}
	callPollInterval = time.Millisecond
	defer func() { callPollInterval = 2 * time.Second }()
	Run(context.Background(), wf, tc)
	if tc.Outcome.Status != StatusCompleted {
		t.Errorf("outcome = %+v, want completed after the callee succeeded", tc.Outcome)
	}
	finished := store.ofKind(events.KindWorkflowCallFinished)
	if len(finished) != 1 || finished[0].Data["status"] != StatusCompleted || finished[0].Data["detail"] != "contained 10.0.0.9" {
		t.Errorf("finished events = %+v, want the callee's terminal status and detail", finished)
	}
}

func TestWorkflowCallWaitTrueFailsWhenCalleeFails(t *testing.T) {
	wf := compileCaller(t, "id: caller\nrepository: none\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      wait: true\n")
	caller := &fakeCaller{statuses: map[int64][]statusAt{
		101: {{StatusParked, "needs an operator"}},
	}}
	store := &recordingStore{}
	tc := &TaskContext{
		Task: &Task{ID: 3, Attempt: 1}, Calls: caller, Store: store,
		Trees: &fakeTrees{}, Log: slog.New(slog.DiscardHandler),
	}
	callPollInterval = time.Millisecond
	defer func() { callPollInterval = 2 * time.Second }()
	Run(context.Background(), wf, tc)
	if !strings.Contains(tc.Task.ParkReason, "needs an operator") {
		t.Fatalf("reason %q; want a park carrying the callee's detail", tc.Task.ParkReason)
	}
	if len(store.ofKind(events.KindWorkflowCallFinished)) != 0 {
		t.Error("a failed call must not record a finished event")
	}
}

func TestWorkflowCallWaitTrueStartFailure(t *testing.T) {
	wf := compileCaller(t, "id: caller\nrepository: none\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: callee\n      wait: true\n")
	caller := &fakeCaller{startErr: errors.New("callee is not enabled for this org")}
	store := &recordingStore{}
	tc := &TaskContext{
		Task: &Task{ID: 3, Attempt: 1}, Calls: caller, Store: store,
		Trees: &fakeTrees{}, Log: slog.New(slog.DiscardHandler),
	}
	Run(context.Background(), wf, tc)
	if !strings.Contains(tc.Task.ParkReason, "callee is not enabled") {
		t.Fatalf("reason %q; want the start failure parked", tc.Task.ParkReason)
	}
}

func TestWorkflowCallSettingsValidation(t *testing.T) {
	for _, test := range []struct{ name, yaml, want string }{
		{"no workflow named", "id: w\nrepository: none\nsteps:\n  - type: workflow.call\n", "settings.workflow is required"},
		{"works in a repository-free workflow", "id: w\nrepository: none\nsteps:\n  - type: workflow.call\n    settings:\n      workflow: other\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDefinition(test.yaml, callRegistry())
			if test.want == "" {
				if err != nil {
					t.Fatalf("ParseDefinition() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseDefinition() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
