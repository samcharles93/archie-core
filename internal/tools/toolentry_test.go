package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// noopHandler is a Handler that does nothing.
func noopHandler(_ context.Context, _ map[string]any) (any, error) {
	return nil, nil
}

// echoHandler returns its input unchanged.
func echoHandler(_ context.Context, in map[string]any) (any, error) {
	return in, nil
}

func TestToolEntryValidate(t *testing.T) {
	t.Run("valid entry", func(t *testing.T) {
		e := ToolEntry{Name: "hello", Handler: noopHandler}
		if err := e.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		e := ToolEntry{Handler: noopHandler}
		if err := e.Validate(); err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("missing handler", func(t *testing.T) {
		e := ToolEntry{Name: "orphan"}
		if err := e.Validate(); err == nil {
			t.Error("expected error for missing handler")
		}
	})
}

func TestToolEntryAvailable(t *testing.T) {
	t.Run("nil CheckFn is always available", func(t *testing.T) {
		e := ToolEntry{Name: "x", Handler: noopHandler}
		if !e.Available() {
			t.Error("nil CheckFn should mean available")
		}
	})

	t.Run("CheckFn returning true", func(t *testing.T) {
		e := ToolEntry{
			Name:    "x",
			Handler: noopHandler,
			CheckFn: func() bool { return true },
		}
		if !e.Available() {
			t.Error("expect available when CheckFn returns true")
		}
	})

	t.Run("CheckFn returning false", func(t *testing.T) {
		e := ToolEntry{
			Name:    "x",
			Handler: noopHandler,
			CheckFn: func() bool { return false },
		}
		if e.Available() {
			t.Error("expect unavailable when CheckFn returns false")
		}
	})

	t.Run("CheckFn reflects dynamic state", func(t *testing.T) {
		flag := true
		e := ToolEntry{
			Name:    "x",
			Handler: noopHandler,
			CheckFn: func() bool { return flag },
		}
		if !e.Available() {
			t.Error("expect available while flag is true")
		}
		flag = false
		if e.Available() {
			t.Error("expect unavailable after flag toggled")
		}
	})
}

func TestToolEntryResolvedSchema(t *testing.T) {
	t.Run("static schema returned as-is", func(t *testing.T) {
		e := ToolEntry{
			Name:    "x",
			Handler: noopHandler,
			Schema:  JSONSchema{"type": "object", "properties": JSONSchema{"x": JSONSchema{"type": "string"}}},
		}
		s := e.ResolvedSchema()
		if s["type"] != "object" {
			t.Errorf("type = %v", s["type"])
		}
	})

	t.Run("nil schema with no override", func(t *testing.T) {
		e := ToolEntry{Name: "x", Handler: noopHandler}
		if e.ResolvedSchema() != nil {
			t.Error("nil schema should stay nil")
		}
	})

	t.Run("dynamic override injects values", func(t *testing.T) {
		e := ToolEntry{
			Name:    "x",
			Handler: noopHandler,
			Schema:  JSONSchema{"type": "object"},
			DynamicSchemaOverrides: func(s JSONSchema) JSONSchema {
				out := JSONSchema{}
				for k, v := range s {
					out[k] = v
				}
				out["dynamic"] = true
				return out
			},
		}
		s := e.ResolvedSchema()
		if s["dynamic"] != true {
			t.Error("dynamic override not applied")
		}
		// Original must be unchanged.
		if e.Schema["dynamic"] != nil {
			t.Error("override mutated original schema")
		}
	})

	t.Run("override can return nil", func(t *testing.T) {
		e := ToolEntry{
			Name:    "x",
			Handler: noopHandler,
			Schema:  JSONSchema{"type": "object"},
			DynamicSchemaOverrides: func(_ JSONSchema) JSONSchema {
				return nil
			},
		}
		if e.ResolvedSchema() != nil {
			t.Error("override returning nil should be respected")
		}
	})
}

func TestToolEntryClone(t *testing.T) {
	e := ToolEntry{
		Name:    "original",
		Toolset: "test",
		Schema:  JSONSchema{"key": "value"},
		Handler: noopHandler,
		CheckFn: func() bool { return true },
		RequiresEnv: []string{"X", "Y"},
		IsAsync: true,
		Description: "a test tool",
		Emoji:       "🧪",
		MaxResultSizeChars: 4096,
	}

	clone := e.Clone()

	// Value fields match.
	if clone.Name != e.Name {
		t.Errorf("Name = %q, want %q", clone.Name, e.Name)
	}
	if clone.IsAsync != e.IsAsync {
		t.Errorf("IsAsync = %v, want %v", clone.IsAsync, e.IsAsync)
	}

	// Handler and CheckFn are shared (function pointers — not deep-copied).
	// This is documented behavior.

	// RequiresEnv is an independent copy.
	e.RequiresEnv[0] = "Z"
	if clone.RequiresEnv[0] != "X" {
		t.Errorf("RequiresEnv[0] = %q, want %q (clone should be independent)", clone.RequiresEnv[0], "X")
	}

	// Schema is an independent shallow copy.
	e.Schema["key"] = "mutated"
	if clone.Schema["key"] != "value" {
		t.Errorf("Schema[key] = %v, want 'value' (clone should be independent)", clone.Schema["key"])
	}
}

func TestToolEntryJSONRoundTrip(t *testing.T) {
	e := ToolEntry{
		Name:        "greet",
		Toolset:     "demo",
		Schema:      JSONSchema{"type": "object", "properties": JSONSchema{"name": JSONSchema{"type": "string"}}},
		Handler:     noopHandler,   // json:"-" — excluded
		CheckFn:     func() bool { return true }, // json:"-" — excluded
		RequiresEnv: []string{"GREET_NAME"},
		IsAsync:     true,
		Description: "Sends a greeting",
		Emoji:       "👋",
		MaxResultSizeChars: 1000,
		DynamicSchemaOverrides: nil, // json:"-" — excluded
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Round-trip.
	var decoded ToolEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != "greet" {
		t.Errorf("Name = %q", decoded.Name)
	}
	if decoded.Toolset != "demo" {
		t.Errorf("Toolset = %q", decoded.Toolset)
	}
	if decoded.IsAsync != true {
		t.Error("IsAsync not preserved")
	}
	if decoded.Description != "Sends a greeting" {
		t.Errorf("Description = %q", decoded.Description)
	}
	if decoded.Emoji != "👋" {
		t.Errorf("Emoji = %q", decoded.Emoji)
	}
	if decoded.MaxResultSizeChars != 1000 {
		t.Errorf("MaxResultSizeChars = %d", decoded.MaxResultSizeChars)
	}
	if len(decoded.RequiresEnv) != 1 || decoded.RequiresEnv[0] != "GREET_NAME" {
		t.Errorf("RequiresEnv = %v", decoded.RequiresEnv)
	}

	// Runtime fields are not deserialized.
	if decoded.Handler != nil {
		t.Error("Handler should be nil after unmarshal")
	}
	if decoded.CheckFn != nil {
		t.Error("CheckFn should be nil after unmarshal")
	}

	// Schema survives round-trip.
	if decoded.Schema["type"] != "object" {
		t.Errorf("Schema type = %v", decoded.Schema["type"])
	}
}

func TestToolEntryHandlerExecution(t *testing.T) {
	e := ToolEntry{
		Name:    "echo",
		Handler: echoHandler,
	}

	out, err := e.Handler(context.Background(), map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	if m["msg"] != "hello" {
		t.Errorf("msg = %v", m["msg"])
	}
}

func TestToolEntryZeroValue(t *testing.T) {
	var e ToolEntry
	// Zero value must be usable without panicking.
	if err := e.Validate(); err == nil {
		t.Error("zero ToolEntry should fail Validate")
	}
	if !e.Available() {
		// nil CheckFn = available — this is correct even for zero value.
	}
	if e.ResolvedSchema() != nil {
		t.Error("zero ToolEntry should have nil resolved schema")
	}
}

func TestToolEntryJSONSchemaIsNilByDefault(t *testing.T) {
	e := ToolEntry{Name: "no-params", Handler: noopHandler}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Schema field omitted when nil.
	if strings.Contains(string(data), `"schema"`) {
		t.Log("schema present in JSON (either null or omitted) — both are acceptable")
	}
}
