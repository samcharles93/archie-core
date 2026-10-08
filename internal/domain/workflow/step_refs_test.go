package workflow

import (
	"slices"
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

// TestRenderSettingsPreservesTypes pins the reference contract between steps:
// a setting that is exactly one reference takes the referenced value's type,
// so booleans, numbers, arrays and objects survive; a reference embedded in
// prose is interpolated as text, which is the only thing a string can hold.
func TestRenderSettingsPreservesTypes(t *testing.T) {
	src := `
flag: "{{ steps.plan.result.fit }}"
count: "{{ steps.plan.result.count }}"
items: "{{ steps.plan.result.labels }}"
nested: "{{ steps.plan.result.meta }}"
whole: "{{ steps.plan.result }}"
text: "fit is {{ steps.plan.result.fit }}"
prose: "labels: {{ steps.plan.result.labels }}"
absent: "{{ steps.plan.result.missing }}"
raw: "{{ steps.plan.summary }}"
`
	var settings yaml.Node
	if err := yaml.Unmarshal([]byte(src), &settings); err != nil {
		t.Fatal(err)
	}
	tc := &TaskContext{Task: &Task{Title: "t"}, stepResults: map[string]StepResult{
		"plan": {
			Summary: "the plan",
			Result: map[string]any{
				"fit":    true,
				"count":  float64(3),
				"labels": []any{"a", "b"},
				"meta":   map[string]any{"k": "v"},
			},
		},
	}}
	var got struct {
		Flag   bool              `yaml:"flag"`
		Count  int               `yaml:"count"`
		Items  []string          `yaml:"items"`
		Nested map[string]string `yaml:"nested"`
		Whole  map[string]any    `yaml:"whole"`
		Text   string            `yaml:"text"`
		Prose  string            `yaml:"prose"`
		Absent string            `yaml:"absent"`
		Raw    string            `yaml:"raw"`
	}
	rendered := renderSettings(settings, tc)
	if err := rendered.Decode(&got); err != nil {
		t.Fatalf("decode rendered settings: %v", err)
	}
	if !got.Flag {
		t.Error("flag: a boolean reference did not survive as a boolean")
	}
	if got.Count != 3 {
		t.Errorf("count = %d, want 3", got.Count)
	}
	if !slices.Equal(got.Items, []string{"a", "b"}) {
		t.Errorf("items = %v, want [a b]", got.Items)
	}
	if got.Nested["k"] != "v" {
		t.Errorf("nested = %v, want k=v", got.Nested)
	}
	if got.Whole["fit"] != true || got.Whole["count"] != 3 {
		t.Errorf("whole = %v, want the whole result object", got.Whole)
	}
	if got.Text != "fit is true" {
		t.Errorf("text = %q, want %q", got.Text, "fit is true")
	}
	if got.Prose != `labels: ["a","b"]` {
		t.Errorf("prose = %q, want the array as text", got.Prose)
	}
	if got.Absent != "" {
		t.Errorf("absent = %q, want empty", got.Absent)
	}
	if got.Raw != "the plan" {
		t.Errorf("raw = %q, want %q", got.Raw, "the plan")
	}
	// The definition is not mutated: rendering works on a copy.
	if original := yamlString(t, settings); !strings.Contains(original, "{{ steps.plan.summary }}") {
		t.Fatalf("rendering changed the definition: %s", original)
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
