package memory

import (
	"encoding/json"
	"testing"
)

func TestNormalizeToolSchema_Bare(t *testing.T) {
	raw := map[string]any{
		"name":        "search_memory",
		"description": "Search stored memory for relevant context.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
		"extra_unknown_field": "ignored",
	}

	got, err := NormalizeToolSchema(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "search_memory" {
		t.Errorf("Name = %q, want %q", got.Name, "search_memory")
	}
	if got.Description != "Search stored memory for relevant context." {
		t.Errorf("Description = %q, want the bare description", got.Description)
	}
	if got.Parameters == nil {
		t.Fatal("Parameters should not be nil")
	}
	if got.Parameters["type"] != "object" {
		t.Errorf("Parameters[type] = %v, want %q", got.Parameters["type"], "object")
	}
}

func TestNormalizeToolSchema_WrappedOpenAI(t *testing.T) {
	raw := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "add_memory",
			"description": "Add a fact to memory.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{"type": "string"},
				},
			},
		},
	}

	got, err := NormalizeToolSchema(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "add_memory" {
		t.Errorf("Name = %q, want %q", got.Name, "add_memory")
	}
	if got.Description != "Add a fact to memory." {
		t.Errorf("Description = %q, want the wrapped description", got.Description)
	}
	if got.Parameters == nil || got.Parameters["type"] != "object" {
		t.Errorf("Parameters not extracted from wrapped function object: %+v", got.Parameters)
	}
}

func TestNormalizeToolSchema_BareWithoutParameters(t *testing.T) {
	raw := map[string]any{"name": "list_memories"}

	got, err := NormalizeToolSchema(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "list_memories" {
		t.Errorf("Name = %q, want %q", got.Name, "list_memories")
	}
	if got.Description != "" {
		t.Errorf("Description = %q, want empty", got.Description)
	}
	if got.Parameters != nil {
		t.Errorf("Parameters = %+v, want nil", got.Parameters)
	}
}

func TestNormalizeToolSchema_NilInput(t *testing.T) {
	if _, err := NormalizeToolSchema(nil); err == nil {
		t.Fatal("expected error for nil input, got nil")
	}
}

func TestNormalizeToolSchema_MissingName(t *testing.T) {
	cases := []map[string]any{
		{"description": "no name here"},
		{"name": ""},
		{"name": 42}, // wrong type
	}
	for _, raw := range cases {
		if _, err := NormalizeToolSchema(raw); err == nil {
			t.Errorf("expected error for raw=%+v, got nil", raw)
		}
	}
}

func TestNormalizeToolSchema_WrappedMissingFunctionObject(t *testing.T) {
	raw := map[string]any{"type": "function"}
	if _, err := NormalizeToolSchema(raw); err == nil {
		t.Fatal("expected error when wrapped schema has no function object")
	}
}

func TestNormalizeToolSchema_UnsupportedType(t *testing.T) {
	raw := map[string]any{
		"type": "retrieval",
		"function": map[string]any{
			"name": "whatever",
		},
	}
	if _, err := NormalizeToolSchema(raw); err == nil {
		t.Fatal("expected error for unsupported top-level type")
	}
}

func TestNormalizeToolSchema_WrappedFunctionMissingName(t *testing.T) {
	raw := map[string]any{
		"type":     "function",
		"function": map[string]any{"description": "no name"},
	}
	if _, err := NormalizeToolSchema(raw); err == nil {
		t.Fatal("expected error when wrapped function object has no name")
	}
}

func TestNormalizeToolSchema_RoundTripsThroughJSON(t *testing.T) {
	raw := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "delete_memory",
			"description": "Delete a stored fact.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
			},
		},
	}

	got, err := NormalizeToolSchema(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var roundTripped NormalizedTool
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if roundTripped.Name != got.Name {
		t.Errorf("round-tripped Name = %q, want %q", roundTripped.Name, got.Name)
	}
	if roundTripped.Description != got.Description {
		t.Errorf("round-tripped Description = %q, want %q", roundTripped.Description, got.Description)
	}
	if roundTripped.Parameters["type"] != got.Parameters["type"] {
		t.Errorf("round-tripped Parameters = %+v, want %+v", roundTripped.Parameters, got.Parameters)
	}

	// Re-normalizing bare JSON produced from the canonical form (name +
	// parameters at the top level) must succeed and match.
	var asBareMap map[string]any
	if err := json.Unmarshal(data, &asBareMap); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}
	reNormalized, err := NormalizeToolSchema(asBareMap)
	if err != nil {
		t.Fatalf("re-normalizing canonical JSON failed: %v", err)
	}
	if reNormalized.Name != got.Name {
		t.Errorf("re-normalized Name = %q, want %q", reNormalized.Name, got.Name)
	}
}
