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
