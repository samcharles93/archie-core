package workflow

import (
	"strings"
	"testing"
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
