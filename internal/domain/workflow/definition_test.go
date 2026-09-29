package workflow

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

func TestParseDefinitionRejectsUnsafeDefinitions(t *testing.T) {
	registry := BuiltinStepRegistry()
	for _, test := range []struct{ name, yaml, want string }{
		{"unknown step", "id: custom\nsteps:\n  - type: shell\n", "unknown type"},
		{"malformed settings", "id: custom\nsteps:\n  - type: implement.prepare\n    settings:\n      command: rm -rf /\n", "accepts no settings"},
		{"unknown field", "id: custom\nscript: arbitrary-go\nsteps:\n  - type: implement.prepare\n", "field script not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDefinition(test.yaml, registry)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseDefinition() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseDefinitionAllowsRepeatingAnOperation(t *testing.T) {
	definition, err := ParseDefinition("id: custom\nsteps:\n  - type: implement.prepare\n  - type: implement.prepare\n", BuiltinStepRegistry())
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if len(definition.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(definition.Steps))
	}
}

// TestDefinitionIDReadsTheDeclaredWorkflowName covers the identity a stored
// definition carries: DefinitionID answers "which workflow is this" for a
// reader holding only YAML, without a step registry and without rejecting a
// definition written against a vocabulary this process does not have (which is
// why a daemon cannot answer it by compiling).
func TestDefinitionIDReadsTheDeclaredWorkflowName(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		err  string
	}{
		{
			name: "the shipped feasibility definition names itself",
			src:  "id: feasibility\nsteps:\n  - type: feasibility.assess\n",
			want: "feasibility",
		},
		{
			name: "an unregistered step type is still an answerable id",
			src:  "id: operator-local\nsteps:\n  - type: plugin.step\n",
			want: "operator-local",
		},
		{
			name: "a definition with no id is refused",
			src:  "steps:\n  - type: feasibility.assess\n",
			err:  "id is required",
		},
		{
			name: "malformed YAML is refused",
			src:  "id: [unterminated\n",
			err:  "parse workflow YAML",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DefinitionID(tt.src)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("DefinitionID() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DefinitionID: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DefinitionID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShippedDefinitionsEmitDeclaredInputsForPRReview(t *testing.T) {
	entry, ok := ShippedDefinitions().DefinitionByID("pr-review")
	if !ok {
		t.Fatal("missing shipped workflow \"pr-review\"")
	}
	iface, err := task.ParseWorkflowInterface(entry.YAML)
	if err != nil {
		t.Fatalf("ParseWorkflowInterface: %v", err)
	}
	spec, ok := iface.Inputs["pr_number"]
	if !ok {
		t.Fatal("pr-review does not declare a pr_number input")
	}
	if spec.Type != "number" || !spec.Required {
		t.Fatalf("pr_number input = %+v, want {Type: number, Required: true}", spec)
	}
}

func TestShippedDefinitionsOmitInputsBlockForWorkflowsThatDeclareNone(t *testing.T) {
	entry, ok := ShippedDefinitions().DefinitionByID("implement")
	if !ok {
		t.Fatal("missing shipped workflow \"implement\"")
	}
	if strings.Contains(entry.YAML, "inputs:") {
		t.Fatalf("implement's generated YAML unexpectedly carries an inputs: block:\n%s", entry.YAML)
	}
}

func TestShippedDefinitionsPreserveStageOrder(t *testing.T) {
	registry := BuiltinStepRegistry()
	for id, legacy := range legacyBuiltinWorkflows() {
		entry, ok := ShippedDefinitions().DefinitionByID(id)
		if !ok {
			t.Fatalf("missing shipped workflow %q", id)
		}
		compiled, err := ParseAndCompile(entry.YAML, registry)
		if err != nil {
			t.Fatalf("compile shipped workflow %q: %v", id, err)
		}
		if len(compiled.Stages) != len(legacy.Stages) {
			t.Fatalf("%s stage count = %d, want %d", id, len(compiled.Stages), len(legacy.Stages))
		}
		for i := range legacy.Stages {
			if compiled.Stages[i].Name != legacy.Stages[i].Name {
				t.Fatalf("%s stage %d = %q, want %q", id, i, compiled.Stages[i].Name, legacy.Stages[i].Name)
			}
		}
	}
}
