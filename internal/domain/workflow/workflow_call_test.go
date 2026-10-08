package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// callSpy records the key every call site started its child under, and models
// the store's own idempotency: one child per call key.
type callSpy struct {
	mu       sync.Mutex
	keys     []string
	children map[string]*task.Task
}

func (c *callSpy) StartCall(_ context.Context, callerTaskID int64, callKey, _ string, _ map[string]any) (*task.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, callKey)
	child, ok := c.children[callKey]
	if !ok {
		child = &task.Task{ID: int64(len(c.children) + 1), CallParentTaskID: callerTaskID}
		c.children[callKey] = child
	}
	return child, nil
}

func (c *callSpy) CallStatus(context.Context, int64, int64) (string, string, map[string]any, error) {
	return StatusCompleted, "", nil, nil
}

func (c *callSpy) startedKeys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.keys)
}

// TestWorkflowCallKeyIsTheDeclaredPath pins the call's durable identity: the
// step's declared path plus the callee it starts. Two runs of one call site --
// a retry of the stage, a lost EnqueueCallTask reply -- key on the same value
// and start one child, and neither a sibling call site nor the same path under
// a re-pinned definition reuses another call's child.
func TestWorkflowCallKeyIsTheDeclaredPath(t *testing.T) {
	registry := StepRegistry{WorkflowCallStepName: newWorkflowCallStage}
	tests := []struct {
		name string
		def  string
		// repin is the definition a later run compiles instead. One task can
		// run a second definition -- workflow.handoff and human.approve set the
		// task's workflow, and the daemon re-pins when the pin's id differs --
		// so a call path that collides there must not reuse the first callee.
		repin     string
		wantKeys  []string
		wantRepin []string
		wantErr   string
	}{
		{
			name: "a call step keys on its declared id and the callee it starts",
			def: `id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee}}
`,
			wantKeys: []string{"check@callee"},
		},
		{
			name: "a call in a parallel branch keys on its path within the run",
			def: `id: caller
repository: none
steps:
  - id: fanout
    parallel:
      a: [{id: check, type: workflow.call, settings: {workflow: callee}}]
      b: [{id: verify, type: workflow.call, settings: {workflow: callee}}]
`,
			wantKeys: []string{"fanout/a/check@callee", "fanout/b/verify@callee"},
		},
		{
			name: "the same path under a re-pinned definition keys on its callee",
			def: `id: caller
repository: none
steps:
  - {id: classify, type: workflow.call, settings: {workflow: callee-a}}
`,
			repin: `id: caller
repository: none
steps:
  - {id: classify, type: workflow.call, settings: {workflow: callee-b}}
`,
			wantKeys:  []string{"classify@callee-a"},
			wantRepin: []string{"classify@callee-b"},
		},
		{
			// A step id may spell a step type: it is a stable identifier like
			// any other, and the path it declares is the call's identity.
			name: "a step id that spells the step type is accepted",
			def: `id: caller
repository: none
steps:
  - {id: workflow.call, type: workflow.call, settings: {workflow: callee}}
`,
			wantKeys: []string{"workflow.call@callee"},
		},
		{
			// The step's id is the call's identity, so an id-less call step is
			// refused where its definition is compiled -- which is also the path
			// a pinned definition takes.
			name: "a call step with no id is refused",
			def: `id: caller
repository: none
steps:
  - type: workflow.call
    settings: {workflow: callee}
`,
			// The whole message, so the refusal still names the step it
			// refused: this is where a pinned definition is compiled.
			wantErr: `workflow "caller" step 1: "workflow.call" needs an id`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &callSpy{children: map[string]*task.Task{}}
			tc := &TaskContext{Task: &task.Task{ID: 7}, Calls: spy, Log: slog.Default()}
			// Twice: a requeued attempt re-runs the call site.
			second := tt.def
			if tt.repin != "" {
				second = tt.repin
			}
			for i, def := range []string{tt.def, second} {
				wf, err := ParseAndCompile(def, registry)
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("compile err = %v, want %q", err, tt.wantErr)
					}
					continue
				}
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				before := len(spy.startedKeys())
				if err := wf.Stages[0].Run(context.Background(), tc); err != nil {
					t.Fatalf("run: %v", err)
				}
				want := tt.wantKeys
				if i == 1 && tt.repin != "" {
					want = tt.wantRepin
				}
				got := spy.startedKeys()[before:]
				// Branches finish in whatever order; the keys a run used, not
				// their order, are the behaviour.
				slices.Sort(got)
				if !slices.Equal(got, want) {
					t.Fatalf("run %d keys = %q, want %q", i+1, got, want)
				}
			}
			if tt.wantErr != "" {
				if keys := spy.startedKeys(); len(keys) != 0 {
					t.Fatalf("started %q, want no call", keys)
				}
				return
			}
			if want := len(tt.wantKeys) + len(tt.wantRepin); len(spy.children) != want {
				t.Fatalf("started %d children, want %d", len(spy.children), want)
			}
		})
	}
}

// TestWorkflowCallWithoutADeclaredPathRefuses pins the one state a call cannot
// key on: a step run with no declared path. The store's unique index ignores
// an empty key, so without the refusal every attempt would start a fresh child
// -- the duplicate the key exists to prevent.
func TestWorkflowCallWithoutADeclaredPathRefuses(t *testing.T) {
	spy := &callSpy{children: map[string]*task.Task{}}
	tc := &TaskContext{Task: &task.Task{ID: 7}, Calls: spy, Log: slog.Default()}
	err := runWorkflowCall(context.Background(), workflowCallSettings{Workflow: "callee"}, tc)
	if err == nil || !strings.Contains(err.Error(), "no declared path") {
		t.Fatalf("err = %v, want a refusal naming the missing path", err)
	}
	if keys := spy.startedKeys(); len(keys) != 0 {
		t.Fatalf("started %q, want no call", keys)
	}
}

// TestWorkflowCallRecordsTheSiteNotTheKey pins the two values apart: the key
// that identifies the child carries the callee, while the step row and the
// call's events are named and staged for the call site, so a rail pairing an
// event with its step by stage can still attach them.
func TestWorkflowCallRecordsTheSiteNotTheKey(t *testing.T) {
	registry := StepRegistry{WorkflowCallStepName: newWorkflowCallStage}
	wf, err := ParseAndCompile(`id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee}}
`, registry)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	store := &fakeStepStore{}
	spy := &callSpy{children: map[string]*task.Task{}}
	tc := &TaskContext{Task: &task.Task{ID: 7, Attempt: 1}, Store: store, StepID: 3, Calls: spy, Log: slog.Default()}
	if err := wf.Stages[0].Run(context.Background(), tc); err != nil {
		t.Fatalf("run: %v", err)
	}
	if keys := spy.startedKeys(); !slices.Equal(keys, []string{"check@callee"}) {
		t.Fatalf("keys = %q, want [check@callee]", keys)
	}
	if len(store.started) != 1 || store.started[0].Kind != task.StepKindCall || store.started[0].Name != "check" {
		t.Fatalf("started = %#v, want one call step named check", store.started)
	}
	if len(store.events) != 1 || store.events[0].Kind != events.KindWorkflowCallStarted || store.events[0].Stage != "check" {
		t.Fatalf("events = %#v, want one %s staged check", store.events, events.KindWorkflowCallStarted)
	}
}

func TestValidateWorkflowCallsWalksBranches(t *testing.T) {
	registry := StepRegistry{
		AgentRunStepName:     newAgentRunStage,
		FinishStepName:       newFinishStage,
		WorkflowCallStepName: newWorkflowCallStage,
	}
	tests := []struct {
		name    string
		defs    map[string]string
		wantErr string
	}{
		{
			name: "a branch call to a defined workflow is accepted",
			defs: map[string]string{
				"caller": `id: caller
repository: none
steps:
  - parallel:
      a: [{id: check, type: workflow.call, settings: {workflow: callee}}]
      b: [{type: agent.run, settings: {mission: m, read_only: true}}]
`,
				"callee": `id: callee
repository: none
steps:
  - type: workflow.finish
`,
			},
		},
		{
			name: "a branch call to an unknown workflow is refused naming the step",
			defs: map[string]string{
				"caller": `id: caller
repository: none
steps:
  - parallel:
      a: [{id: check, type: workflow.call, settings: {workflow: missing}}]
      b: [{type: agent.run, settings: {mission: m, read_only: true}}]
`,
			},
			wantErr: `workflow "caller" step 1 branch "a" step 1 calls "missing", which is not defined`,
		},
		{
			name: "a cycle through branch calls is refused",
			defs: map[string]string{
				"a": `id: a
repository: none
steps:
  - parallel:
      x: [{id: check, type: workflow.call, settings: {workflow: b}}]
      y: [{type: agent.run, settings: {mission: m, read_only: true}}]
`,
				"b": `id: b
repository: none
steps:
  - id: call-back
    type: workflow.call
    settings: {workflow: a}
`,
			},
			wantErr: "calls itself through",
		},
		{
			name: "a later step reads a waited call's declared output",
			defs: map[string]string{
				"caller": `id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee, wait: true}}
  - type: agent.run
    settings: {mission: "{{ steps.check.result.verdict }}"}
`,
				"callee": `id: callee
repository: none
outputs: {verdict: {type: string}}
steps:
  - type: workflow.finish
`,
			},
		},
		{
			name: "a call result field the callee does not declare is refused",
			defs: map[string]string{
				"caller": `id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee, wait: true}}
  - type: agent.run
    settings: {mission: "{{ steps.check.result.verdit }}"}
`,
				"callee": `id: callee
repository: none
outputs: {verdict: {type: string}}
steps:
  - type: workflow.finish
`,
			},
			wantErr: `the workflow call "check" declares no output "verdit"`,
		},
		{
			name: "a call that does not wait has no result",
			defs: map[string]string{
				"caller": `id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee, wait: false}}
  - type: agent.run
    settings: {mission: "{{ steps.check.result.verdict }}"}
`,
				"callee": `id: callee
repository: none
outputs: {verdict: {type: string}}
steps:
  - type: workflow.finish
`,
			},
			wantErr: `call "check" does not wait, so it has no result`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collection := task.WorkflowDefinitionCollection{}
			for _, id := range slices.Sorted(maps.Keys(tt.defs)) {
				collection.Definitions = append(collection.Definitions, task.WorkflowDefinitionEntry{ID: id, YAML: tt.defs[id]})
			}
			err := ValidateDefinitionCollection(collection, registry)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// wobblyCaller models a dependency that is briefly unavailable: CallStatus
// fails failReads times before it answers, and counts the reads and the
// children it was asked to start.
type wobblyCaller struct {
	mu        sync.Mutex
	failReads int
	failWith  error
	reads     int
	started   int
	status    string
	detail    string
	outputs   map[string]any
}

func (c *wobblyCaller) StartCall(_ context.Context, callerTaskID int64, _, _ string, _ map[string]any) (*task.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started++
	return &task.Task{ID: 42, CallParentTaskID: callerTaskID}, nil
}

func (c *wobblyCaller) CallStatus(context.Context, int64, int64) (string, string, map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	if c.reads <= c.failReads {
		return "", "", nil, c.failWith
	}
	return c.status, c.detail, c.outputs, nil
}

func (c *wobblyCaller) counts() (reads, started int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads, c.started
}

// shortenCallWaits makes the caller's polls and retry backoff immediate, so a
// test drives the retry loop without sleeping. Restored by t.Cleanup.
func shortenCallWaits(t *testing.T) {
	t.Helper()
	oldPoll, oldBackoff, oldMax := callPollInterval, callReadBackoff, callReadMaxBackoff
	callPollInterval, callReadBackoff, callReadMaxBackoff = time.Millisecond, time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() { callPollInterval, callReadBackoff, callReadMaxBackoff = oldPoll, oldBackoff, oldMax })
}

// TestAwaitCalleeRetriesTransientReadFailures pins that a callee status read
// that fails because the dependency is briefly unavailable is retried, not
// failed: the child is still running, and the caller reads it again.
func TestAwaitCalleeRetriesTransientReadFailures(t *testing.T) {
	shortenCallWaits(t)
	caller := &wobblyCaller{
		failReads: 2,
		failWith:  fmt.Errorf("dial state store: %w", storecontract.ErrUnavailable),
		status:    StatusCompleted,
		detail:    "done",
		outputs:   map[string]any{"report": "written"},
	}
	tc := &TaskContext{Task: &task.Task{ID: 7}, Calls: caller, Log: slog.Default()}
	outputs, err := awaitCallee(context.Background(), workflowCallSettings{Workflow: "callee", Wait: true}, tc, "check", 42)
	if err != nil {
		t.Fatalf("awaitCallee: %v", err)
	}
	if outputs["report"] != "written" {
		t.Fatalf("outputs = %v, want report=written", outputs)
	}
	if reads, _ := caller.counts(); reads != 3 {
		t.Fatalf("reads = %d, want 3 (two retries, then the answer)", reads)
	}
}

// TestAwaitCalleeFailsPermanentReadErrorAtOnce pins the other half: a contract
// error is not retried, because it will fail the same way every time.
func TestAwaitCalleeFailsPermanentReadErrorAtOnce(t *testing.T) {
	shortenCallWaits(t)
	caller := &wobblyCaller{failReads: 5, failWith: storecontract.ErrCallNotYours, status: StatusCompleted}
	tc := &TaskContext{Task: &task.Task{ID: 7}, Calls: caller, Log: slog.Default()}
	_, err := awaitCallee(context.Background(), workflowCallSettings{Workflow: "callee", Wait: true}, tc, "check", 42)
	if !errors.Is(err, storecontract.ErrCallNotYours) {
		t.Fatalf("err = %v, want ErrCallNotYours", err)
	}
	if reads, _ := caller.counts(); reads != 1 {
		t.Fatalf("reads = %d, want 1 (no retry of a contract error)", reads)
	}
}

// TestAwaitCalleeRetryStaysInsideTheDeadline pins the bound on recovery: an
// outage that outlasts the caller's budget ends the wait there rather than
// retrying past it.
func TestAwaitCalleeRetryStaysInsideTheDeadline(t *testing.T) {
	shortenCallWaits(t)
	caller := &wobblyCaller{failReads: 1 << 30, failWith: fmt.Errorf("dial: %w", storecontract.ErrUnavailable)}
	tc := &TaskContext{Task: &task.Task{ID: 7}, Calls: caller, Log: slog.Default()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := awaitCallee(ctx, workflowCallSettings{Workflow: "callee", Wait: true}, tc, "check", 42)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
}

// TestWorkflowCallRetriesARefusedReadWithoutASecondChild pins the whole step:
// a wait:true call whose status reads are briefly refused still succeeds, the
// caller starts exactly one child, and the call step is closed once.
func TestWorkflowCallRetriesARefusedReadWithoutASecondChild(t *testing.T) {
	shortenCallWaits(t)
	registry := StepRegistry{WorkflowCallStepName: newWorkflowCallStage}
	wf, err := ParseAndCompile(`id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee, wait: true}}
`, registry)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	store := &fakeStepStore{}
	caller := &wobblyCaller{
		failReads: 1,
		failWith:  fmt.Errorf("dial: %w", storecontract.ErrUnavailable),
		status:    StatusCompleted,
		outputs:   map[string]any{"verdict": "go"},
	}
	tc := &TaskContext{Task: &task.Task{ID: 7, Attempt: 1}, Store: store, StepID: 3, Calls: caller, Log: slog.Default()}
	if err := wf.Stages[0].Run(context.Background(), tc); err != nil {
		t.Fatalf("run: %v", err)
	}
	reads, started := caller.counts()
	if started != 1 {
		t.Fatalf("started %d children, want 1: a retried read must not start another", started)
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want 2 (one refused, one answer)", reads)
	}
	if tc.stepResult.Result["verdict"] != "go" {
		t.Fatalf("step result = %v, want the callee's outputs", tc.stepResult.Result)
	}
	calls := 0
	for _, s := range store.started {
		if s.Kind == task.StepKindCall {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("recorded %d call steps, want 1", calls)
	}
}
