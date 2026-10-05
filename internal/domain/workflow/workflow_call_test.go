package workflow

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

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
      a: [{type: workflow.call, settings: {workflow: callee}}]
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
      a: [{type: workflow.call, settings: {workflow: missing}}]
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
      x: [{type: workflow.call, settings: {workflow: b}}]
      y: [{type: agent.run, settings: {mission: m, read_only: true}}]
`,
				"b": `id: b
repository: none
steps:
  - type: workflow.call
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
