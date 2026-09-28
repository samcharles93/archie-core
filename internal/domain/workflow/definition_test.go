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
