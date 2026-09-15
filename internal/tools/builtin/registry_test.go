package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools/builtin"
)

func dummyExecutor(_ context.Context, _ json.RawMessage, _ builtin.UIBridge) (builtin.Result, error) {
	return builtin.Result{Content: "ok"}, nil
}

func TestRegistry_Register(t *testing.T) {
	r := builtin.NewRegistry()

	tool := builtin.Tool{
		Schema:  builtin.Schema{Name: "test", Description: "a test tool"},
		Execute: dummyExecutor,
		Source:  "builtin",
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.Count() != 1 {
		t.Fatalf("expected 1 tool, got %d", r.Count())
	}

	// Duplicate registration should fail.
	if err := r.Register(tool); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}

func TestRegistry_Replace(t *testing.T) {
	r := builtin.NewRegistry()

	tool := builtin.Tool{
		Schema:  builtin.Schema{Name: "test", Description: "v1"},
		Execute: dummyExecutor,
		Source:  "builtin",
	}
	_ = r.Register(tool)

	tool.Schema.Description = "v2"
	if err := r.Replace(tool); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := r.Get("test")
	if !ok {
		t.Fatal("tool not found after replace")
	}
	if got.Schema.Description != "v2" {
		t.Fatalf("expected description v2, got %q", got.Schema.Description)
	}
	if r.Count() != 1 {
		t.Fatalf("replace should not duplicate; got count %d", r.Count())
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := builtin.NewRegistry()

	_ = r.Register(builtin.Tool{
		Schema:  builtin.Schema{Name: "a", Description: "tool a"},
		Execute: dummyExecutor,
		Source:  "builtin",
	})
	_ = r.Register(builtin.Tool{
		Schema:  builtin.Schema{Name: "b", Description: "tool b"},
		Execute: dummyExecutor,
		Source:  "builtin",
	})

	r.Unregister("a")

	if r.Count() != 1 {
		t.Fatalf("expected 1 tool after unregister, got %d", r.Count())
	}
	if _, ok := r.Get("a"); ok {
		t.Fatal("tool 'a' should be gone")
	}
}

func TestRegistry_All_PreservesOrder(t *testing.T) {
	r := builtin.NewRegistry()

	names := []string{"alpha", "beta", "gamma"}
	for _, name := range names {
		_ = r.Register(builtin.Tool{
			Schema:  builtin.Schema{Name: name, Description: name},
			Execute: dummyExecutor,
			Source:  "builtin",
		})
	}

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(all))
	}
	for i, tool := range all {
		if tool.Schema.Name != names[i] {
			t.Fatalf("order mismatch at %d: got %q, want %q", i, tool.Schema.Name, names[i])
		}
	}
}

func TestRegistry_Schemas(t *testing.T) {
	r := builtin.NewRegistry()

	_ = r.Register(builtin.Tool{
		Schema: builtin.Schema{
			Name:        "read",
			Description: "Read a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		},
		Execute: dummyExecutor,
		Source:  "builtin",
	})

	schemas := r.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Name != "read" {
		t.Fatalf("expected schema name 'read', got %q", schemas[0].Name)
	}
}

func TestRegistry_Validation(t *testing.T) {
	r := builtin.NewRegistry()

	// Empty name.
	err := r.Register(builtin.Tool{
		Schema:  builtin.Schema{Name: "", Description: "no name"},
		Execute: dummyExecutor,
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}

	// Nil executor.
	err = r.Register(builtin.Tool{
		Schema: builtin.Schema{Name: "test", Description: "no exec"},
	})
	if err == nil {
		t.Fatal("expected error for nil executor")
	}
}
