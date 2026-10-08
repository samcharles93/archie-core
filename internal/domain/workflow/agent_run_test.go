package workflow

import (
	"slices"
	"strings"
	"testing"
)

// TestAgentResultToolSchema covers how an agent.run step's result schema
// becomes a capture tool: none stays none, a good schema keeps its derived
// constraints, and a schema that would not resolve is refused at definition
// time rather than producing a tool the run can never satisfy.
func TestAgentResultToolSchema(t *testing.T) {
	valid := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"needs_change": map[string]any{"type": "boolean"},
			"workflow":     map[string]any{"type": "string", "enum": []any{"implement", "tdd"}},
			"reasons":      map[string]any{"type": "string"},
		},
		"required": []any{"needs_change", "workflow", "reasons"},
	}
	tests := []struct {
		name    string
		schema  map[string]any
		wantErr string
	}{
		{name: "no schema", schema: nil},
		{name: "valid schema", schema: valid},
		{name: "not an object", schema: map[string]any{"type": "string"}, wantErr: "must be a JSON Schema with type: object"},
		{
			name: "unresolvable reference",
			schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"x": map[string]any{"$ref": "#/definitions/missing"}},
			},
			wantErr: "must be a valid JSON Schema",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, err := agentResultTool(tt.schema)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.schema == nil {
				if tool != nil {
					t.Fatalf("tool = %#v, want nil for no schema", tool)
				}
				return
			}
			if tool == nil {
				t.Fatal("tool = nil, want a capture tool")
			}
			if len(tool.RequiredFields) != 3 {
				t.Fatalf("RequiredFields = %v, want the schema's three required fields", tool.RequiredFields)
			}
			if !containsString(tool.NonEmptyStrings, "reasons") && !containsString(tool.NonEmptyStrings, "workflow") {
				t.Fatalf("NonEmptyStrings = %v, want the required string fields", tool.NonEmptyStrings)
			}
			if !containsString(tool.BooleanFields, "needs_change") {
				t.Fatalf("BooleanFields = %v, want needs_change", tool.BooleanFields)
			}
		})
	}
}

func containsString(list []string, want string) bool {
	return slices.Contains(list, want)
}
