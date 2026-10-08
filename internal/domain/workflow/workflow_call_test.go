package workflow

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
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

// TestWorkflowCallKeyIsTheDeclaredPath pins the call's durable identity: two
// runs of one call site -- a retry of the stage, a lost EnqueueCallTask reply
// -- key on the same declared path and start one child, and two call sites in
// one run key on different paths so neither returns the other's child.
func TestWorkflowCallKeyIsTheDeclaredPath(t *testing.T) {
	registry := StepRegistry{WorkflowCallStepName: newWorkflowCallStage}
	tests := []struct {
		name     string
		def      string
		wantKeys []string
		wantErr  string
	}{
		{
			name: "a call step keys on its declared id",
			def: `id: caller
repository: none
steps:
  - {id: check, type: workflow.call, settings: {workflow: callee}}
`,
			wantKeys: []string{"check"},
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
			wantKeys: []string{"fanout/a/check", "fanout/b/verify"},
		},
		{
			// A definition pinned before the rule that a call declares an id
			// compiles and runs: were its path the bare step type, every call
			// site in the run would share one key and the second would return
			// the first's child.
			name: "a call step with no id is refused, not keyed on its type",
			def: `id: caller
repository: none
steps:
  - type: workflow.call
    settings: {workflow: callee}
`,
			wantErr: "declares no id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, err := ParseAndCompile(tt.def, registry)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			spy := &callSpy{children: map[string]*task.Task{}}
			tc := &TaskContext{Task: &task.Task{ID: 7}, Calls: spy, Log: slog.Default()}
			stage := wf.Stages[0]
			// The same stage twice: a requeued attempt re-runs the call site.
			for range 2 {
				if err := stage.Run(context.Background(), tc); err != nil {
					if tt.wantErr == "" || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("err = %v, want %q", err, tt.wantErr)
					}
					if keys := spy.startedKeys(); len(keys) != 0 {
						t.Fatalf("started %q, want no call", keys)
					}
					return
				}
				if tt.wantErr != "" {
					t.Fatalf("err = nil, want %q", tt.wantErr)
				}
			}
			keys := spy.startedKeys()
			first, second := keys[:len(keys)/2], keys[len(keys)/2:]
			// Branches finish in whatever order; the keys a run used, not their
			// order, are the behaviour.
			slices.Sort(first)
			slices.Sort(second)
			if !slices.Equal(first, tt.wantKeys) || !slices.Equal(second, tt.wantKeys) {
				t.Fatalf("keys = %q then %q, want %q both times", first, second, tt.wantKeys)
			}
			if len(spy.children) != len(tt.wantKeys) {
				t.Fatalf("started %d children, want %d", len(spy.children), len(tt.wantKeys))
			}
		})
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
			name: "a call step with no id is refused naming the step",
			defs: map[string]string{
				"caller": `id: caller
repository: none
steps:
  - {type: workflow.call, settings: {workflow: callee}}
`,
				"callee": `id: callee
repository: none
steps:
  - type: workflow.finish
`,
			},
			wantErr: `workflow "caller" step 1: workflow.call declares no id`,
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
