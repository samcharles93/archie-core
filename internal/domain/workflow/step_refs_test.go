package workflow

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStepReferences(t *testing.T) {
	registry := StepRegistry{AgentRunStepName: newAgentRunStage, FinishStepName: newFinishStage}
	tests := []struct {
		name    string
		yaml    string
		wantErr string
		// rendered is the second step's mission after the first step left
		// summary "the plan" and result {verdict: go}.
		rendered string
	}{
		{
			name: "earlier step summary and result resolve",
			yaml: `id: w
repository: none
steps:
  - id: plan
    type: agent.run
    settings: {mission: "plan {{ task.title }}"}
  - type: agent.run
    settings: {mission: "build {{ steps.plan.summary }} / {{ steps.plan.result.verdict }} / {{ steps.plan.result.missing }}"}
`,
			rendered: "build the plan / go / ",
		},
		{
			name: "a later step cannot be referenced",
			yaml: `id: w
repository: none
steps:
  - type: agent.run
    settings: {mission: "{{ steps.plan.summary }}"}
  - id: plan
    type: agent.run
    settings: {mission: x}
`,
			wantErr: `no earlier step has id "plan"`,
		},
		{
			name: "unknown root is refused",
			yaml: `id: w
repository: none
steps:
  - type: agent.run
    settings: {mission: "{{ secrets.token }}"}
`,
			wantErr: "must start with task, inputs or steps",
		},
		{
			name: "duplicate step id is refused",
			yaml: `id: w
repository: none
steps:
  - {id: a, type: workflow.finish}
  - {id: a, type: workflow.finish}
`,
			wantErr: `step id "a" is declared twice`,
		},
		{
			name: "when may only read earlier steps",
			yaml: `id: w
repository: none
steps:
  - {type: workflow.finish, when: "!steps.later.result.fit"}
  - {id: later, type: workflow.finish}
`,
			wantErr: `when: reference {{ steps.later.result.fit }}: no earlier step has id "later"`,
		},
		{
			name: "a branch cannot read a sibling branch",
			yaml: `id: w
repository: none
steps:
  - parallel:
      a: [{id: x, type: agent.run, settings: {mission: m, read_only: true}}]
      b: [{type: agent.run, settings: {mission: "{{ steps.x.summary }}", read_only: true}}]
`,
			wantErr: `branch "b" step 1: reference {{ steps.x.summary }}: no earlier step has id "x"`,
		},
		{
			name: "a branch step must not write the worktree",
			yaml: `id: w
repository: none
steps:
  - parallel:
      a: [{type: agent.run, settings: {mission: m}}]
      b: [{type: agent.run, settings: {mission: m, read_only: true}}]
`,
			wantErr: "cannot run in a parallel branch",
		},
		{
			name: "every branch result is visible after the parallel step",
			yaml: `id: w
repository: none
steps:
  - parallel:
      a: [{id: plan, type: agent.run, settings: {mission: m, read_only: true}}]
      b: [{id: other, type: agent.run, settings: {mission: m, read_only: true}}]
  - type: agent.run
    settings: {mission: "build {{ steps.plan.summary }} / {{ steps.plan.result.verdict }} / {{ steps.plan.result.missing }}"}
`,
			rendered: "build the plan / go / ",
		},
		{
			name: "an undeclared input is refused",
			yaml: `id: w
repository: none
steps:
  - type: agent.run
    settings: {mission: "{{ inputs.ticket }}"}
`,
			wantErr: `reference {{ inputs.ticket }}: the workflow declares no input "ticket"`,
		},
		{
			name: "when may only read a declared input",
			yaml: `id: w
repository: none
inputs: {ticket: {type: string}}
steps:
  - {type: workflow.finish, when: "inputs.other"}
`,
			wantErr: `when: reference {{ inputs.other }}: the workflow declares no input "other"`,
		},
		{
			name: "a result field outside the step's schema is refused",
			yaml: `id: w
repository: none
steps:
  - id: plan
    type: agent.run
    settings: {mission: m, result: {type: object, properties: {verdict: {type: string}}}}
  - type: agent.run
    settings: {mission: "{{ steps.plan.result.verdit }}"}
`,
			wantErr: `step "plan"'s result schema has no field "verdit"`,
		},
		{
			name: "when may only read a declared result field",
			yaml: `id: w
repository: none
steps:
  - id: plan
    type: agent.run
    settings: {mission: m, result: {type: object, properties: {verdict: {type: string}}}}
  - {type: workflow.finish, when: "steps.plan.result.fit"}
`,
			wantErr: `when: reference {{ steps.plan.result.fit }}: step "plan"'s result schema has no field "fit"`,
		},
		{
			name: "retry attempts are bounded",
			yaml: `id: w
repository: none
steps:
  - {type: workflow.finish, retry: {attempts: 50}}
`,
			wantErr: "retry.attempts is 1 to 10",
		},
		{
			name: "on_failure takes park or continue",
			yaml: `id: w
repository: none
steps:
  - {type: workflow.finish, on_failure: retry}
`,
			wantErr: "on_failure is park or continue",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			definition, err := ParseDefinition(tt.yaml, registry)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc := &TaskContext{Task: &Task{Title: "t"}, stepResults: map[string]StepResult{
				"plan": {Summary: "the plan", Result: map[string]any{"verdict": "go"}},
			}}
			rendered := renderSettings(definition.Steps[1].Settings, tc)
			var settings agentRunSettings
			if err := rendered.Decode(&settings); err != nil {
				t.Fatal(err)
			}
			if settings.Mission != tt.rendered {
				t.Fatalf("mission = %q, want %q", settings.Mission, tt.rendered)
			}
			if original := yamlString(t, definition.Steps[1].Settings); !strings.Contains(original, "{{ steps.plan.summary }}") {
				t.Fatalf("rendering changed the definition: %s", original)
			}
		})
	}
}

func yamlString(t *testing.T, node yaml.Node) string {
	t.Helper()
	out, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
