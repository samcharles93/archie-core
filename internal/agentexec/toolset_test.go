package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	aicore "github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/tools"
)

func TestToolSetConvertsRegisteredEntries(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:        "echo",
		Description: "echoes its input",
		Schema:      tools.JSONSchema{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			return input["msg"], nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)

	tool, ok := set["echo"]
	if !ok {
		t.Fatal("ToolSet did not include the \"echo\" entry")
	}
	if tool.Name != "echo" || tool.Description != "echoes its input" {
		t.Errorf("tool = %+v", tool)
	}
	if len(tool.Parameters) == 0 {
		t.Error("Parameters is empty, want the marshaled schema")
	}
}

func TestToolSetHandlerRoundTripsJSON(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name: "add",
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			a, _ := input["a"].(float64)
			b, _ := input["b"].(float64)
			return a + b, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	out, err := set["add"].Execute(context.Background(), `{"a": 2, "b": 3}`)
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q did not decode as JSON: %v", out, err)
	}
	if got != 5 {
		t.Errorf("result = %v, want 5", got)
	}
}

func TestToolSetHandlerPropagatesHandlerError(t *testing.T) {
	reg := tools.NewRegistry()
	wantErr := errors.New("boom")
	if err := reg.Register(tools.ToolEntry{
		Name: "fails",
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, wantErr
		},
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	_, err := set["fails"].Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error from failing handler")
	}
}

func TestToolSetHandlerRejectsMalformedInput(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "noop",
		Handler: func(context.Context, map[string]any) (any, error) { return "ok", nil },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	_, err := set["noop"].Execute(context.Background(), `not json`)
	if err == nil {
		t.Fatal("expected error decoding malformed input JSON")
	}
}

func TestToolSetOmitsUnavailableTools(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "offline",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
		CheckFn: func() bool { return false },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	if _, ok := set["offline"]; ok {
		t.Error("ToolSet included a tool whose CheckFn reports unavailable")
	}
}

func TestToolSetEmptyRegistryReturnsEmptySet(t *testing.T) {
	set := mustBuildToolSet(t, tools.NewRegistry())
	if len(set) != 0 {
		t.Errorf("len(set) = %d, want 0", len(set))
	}
}

func TestToolSetNilRegistryReturnsEmptySet(t *testing.T) {
	set := mustBuildToolSet(t, nil)
	if len(set) != 0 {
		t.Errorf("len(set) = %d, want 0 for nil registry", len(set))
	}
}

func TestToolSetUsesObjectSchemaForParameterlessTools(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "no_args",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}

	parameters := mustBuildToolSet(t, reg)["no_args"].Parameters
	var schema map[string]any
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatalf("Parameters = %s, want JSON object: %v", parameters, err)
	}
	if schema["type"] != "object" {
		t.Fatalf("Parameters = %s, want object schema", parameters)
	}
}

func TestToolSetHandlerReturningNilOutputProducesNullJSON(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "voidy",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	out, err := set["voidy"].Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "null" {
		t.Errorf("output = %q, want \"null\"", out)
	}
}

func TestBuildToolSetReportsInvalidDynamicSchemas(t *testing.T) {
	tests := []struct {
		name  string
		entry tools.ToolEntry
	}{
		{
			name: "schema override panic",
			entry: tools.ToolEntry{
				Name:    "panics",
				Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
				DynamicSchemaOverrides: func(tools.JSONSchema) tools.JSONSchema {
					panic("schema panic")
				},
			},
		},
		{
			name: "unmarshalable schema",
			entry: tools.ToolEntry{
				Name:    "invalid",
				Schema:  tools.JSONSchema{"bad": func() {}},
				Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
			},
		},
		{
			name: "availability panic",
			entry: tools.ToolEntry{
				Name:    "unavailable",
				Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
				CheckFn: func() bool {
					panic("availability panic")
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := tools.NewRegistry()
			if err := registry.Register(tt.entry); err != nil {
				t.Fatal(err)
			}
			var (
				err      error
				panicked any
			)
			func() {
				defer func() { panicked = recover() }()
				_, err = BuildToolSet(registry)
			}()
			if panicked != nil {
				t.Fatalf("BuildToolSet() panicked: %v", panicked)
			}
			if err == nil {
				t.Fatal("BuildToolSet() error = nil, want invalid schema error")
			}
		})
	}
}

func mustBuildToolSet(t *testing.T, registry *tools.Registry) map[string]*aicore.Tool {
	t.Helper()
	set, err := BuildToolSet(registry)
	if err != nil {
		t.Fatal(err)
	}
	return set
}
