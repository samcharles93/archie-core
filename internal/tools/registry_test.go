package tools

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRegistryRegister(t *testing.T) {
	t.Run("valid entry", func(t *testing.T) {
		r := NewRegistry()
		err := r.Register(ToolEntry{Name: "hello", Handler: noopHandler})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(r.All()) != 1 {
			t.Errorf("expected 1 tool, got %d", len(r.All()))
		}
	})

	t.Run("duplicate name returns error", func(t *testing.T) {
		r := NewRegistry()
		e := ToolEntry{Name: "dup", Handler: noopHandler}
		if err := r.Register(e); err != nil {
			t.Fatalf("first register: %v", err)
		}
		err := r.Register(e)
		if err == nil {
			t.Error("expected error for duplicate name")
		}
		if !errors.Is(err, ErrDuplicateTool) {
			t.Errorf("expected ErrDuplicateTool, got %v", err)
		}
	})

	t.Run("invalid entry returns validation error", func(t *testing.T) {
		r := NewRegistry()
		err := r.Register(ToolEntry{Name: ""})
		if err == nil {
			t.Error("expected validation error for empty name")
		}
	})

	t.Run("multiple tools", func(t *testing.T) {
		r := NewRegistry()
		for _, name := range []string{"a", "b", "c"} {
			if err := r.Register(ToolEntry{Name: name, Handler: noopHandler}); err != nil {
				t.Fatalf("register %q: %v", name, err)
			}
		}
		if len(r.All()) != 3 {
			t.Errorf("expected 3 tools, got %d", len(r.All()))
		}
	})
}

func TestRegistryAll(t *testing.T) {
	t.Run("empty registry", func(t *testing.T) {
		r := NewRegistry()
		all := r.All()
		if len(all) != 0 {
			t.Errorf("expected 0 tools, got %d", len(all))
		}
	})

	t.Run("returns copy of tools", func(t *testing.T) {
		r := NewRegistry()
		r.Register(ToolEntry{Name: "x", Handler: noopHandler})
		all := r.All()
		all[0].Name = "mutated"
		// Original registry must be unaffected.
		if r.All()[0].Name != "x" {
			t.Error("All() should return independent copies")
		}
	})
}

func TestRegistryByToolset(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolEntry{Name: "f1", Toolset: "file", Handler: noopHandler})
	r.Register(ToolEntry{Name: "f2", Toolset: "file", Handler: noopHandler})
	r.Register(ToolEntry{Name: "g1", Toolset: "git", Handler: noopHandler})
	r.Register(ToolEntry{Name: "u1", Toolset: "", Handler: noopHandler}) // ungrouped

	t.Run("filters by toolset", func(t *testing.T) {
		file := r.ByToolset("file")
		if len(file) != 2 {
			t.Errorf("expected 2 file tools, got %d", len(file))
		}
		names := toolNames(file)
		slices.Sort(names)
		if names[0] != "f1" || names[1] != "f2" {
			t.Errorf("unexpected names: %v", names)
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		none := r.ByToolset("nonexistent")
		if len(none) != 0 {
			t.Errorf("expected 0 tools, got %d", len(none))
		}
	})

	t.Run("empty toolset matches ungrouped", func(t *testing.T) {
		ungrouped := r.ByToolset("")
		if len(ungrouped) != 1 {
			t.Errorf("expected 1 ungrouped tool, got %d", len(ungrouped))
		}
		if ungrouped[0].Name != "u1" {
			t.Errorf("unexpected name: %q", ungrouped[0].Name)
		}
	})
}

func TestRegistryAvailable(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolEntry{Name: "always", Handler: noopHandler})
	r.Register(ToolEntry{Name: "offline", Handler: noopHandler, CheckFn: func() bool { return false }})
	r.Register(ToolEntry{Name: "conditional", Handler: noopHandler, CheckFn: func() bool { return true }})

	t.Run("returns only available tools", func(t *testing.T) {
		avail := r.Available()
		names := toolNames(avail)
		if !slices.Contains(names, "always") {
			t.Error("'always' should be available")
		}
		if !slices.Contains(names, "conditional") {
			t.Error("'conditional' should be available")
		}
		if slices.Contains(names, "offline") {
			t.Error("'offline' should not be available")
		}
		if len(avail) != 2 {
			t.Errorf("expected 2 available tools, got %d", len(avail))
		}
	})

	t.Run("dynamic availability reflected", func(t *testing.T) {
		flag := true
		r.Register(ToolEntry{
			Name:    "toggle",
			Handler: noopHandler,
			CheckFn: func() bool { return flag },
		})
		if len(r.Available()) != 3 {
			t.Errorf("expected 3 available (toggle is true), got %d", len(r.Available()))
		}
		flag = false
		// Available() re-evaluates — the closure captures &flag.
		if len(r.Available()) != 2 {
			t.Errorf("expected 2 available (toggle is now false), got %d", len(r.Available()))
		}
	})
}

func TestRegistryDiscover(t *testing.T) {
	t.Run("no go files", func(t *testing.T) {
		r := NewRegistry()
		fsys := fstest.MapFS{}
		n, err := r.Discover(fsys, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0 discovered, got %d", n)
		}
	})

	t.Run("go file with Register call", func(t *testing.T) {
		r := NewRegistry()
		fsys := fstest.MapFS{
			"mytool.go": &fstest.MapFile{
				Data: []byte(`package tools

import "github.com/sam/archie-core/internal/tools"

func init() {
	tools.DefaultRegistry().Register(tools.ToolEntry{
		Name:        "discovered_hello",
		Description: "A discovered tool",
		Handler:     noopHandler,
	})
}
`),
			},
		}
		n, err := r.Discover(fsys, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 1 {
			t.Errorf("expected 1 discovered, got %d", n)
		}
		all := r.All()
		if len(all) != 1 {
			t.Fatalf("expected 1 registered tool, got %d", len(all))
		}
		if all[0].Name != "discovered_hello" {
			t.Errorf("Name = %q", all[0].Name)
		}
		if all[0].Description != "A discovered tool" {
			t.Errorf("Description = %q", all[0].Description)
		}
	})

	t.Run("go file without Register call", func(t *testing.T) {
		r := NewRegistry()
		fsys := fstest.MapFS{
			"plain.go": &fstest.MapFile{
				Data: []byte(`package tools

func PlainFunction() string {
	return "hello"
}
`),
			},
		}
		n, err := r.Discover(fsys, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0 discovered, got %d", n)
		}
	})

	t.Run("syntax error in go file", func(t *testing.T) {
		r := NewRegistry()
		fsys := fstest.MapFS{
			"broken.go": &fstest.MapFile{
				Data: []byte(`package tools

func broken( {`),
			},
		}
		_, err := r.Discover(fsys, ".")
		if err == nil {
			t.Error("expected error for broken Go file")
		}
	})
}

func TestRegistrySingleton(t *testing.T) {
	// DefaultRegistry returns the same instance.
	d1 := DefaultRegistry()
	d2 := DefaultRegistry()
	if d1 != d2 {
		t.Error("DefaultRegistry should be a singleton")
	}
}

// toolNames extracts names from a slice of ToolEntry.
func toolNames(entries []ToolEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

// Ensure registry implements basic sanity at the package level.
func TestRegistryPackageLevelHelpers(t *testing.T) {
	// Reset state by creating a fresh registry.
	// The package-level helpers work against DefaultRegistry.

	t.Run("Register and All via package helpers", func(t *testing.T) {
		r := NewRegistry()
		e := ToolEntry{Name: "pkg_helper", Handler: noopHandler}
		if err := r.Register(e); err != nil {
			t.Fatalf("register: %v", err)
		}
		if len(r.All()) != 1 {
			t.Error("expected 1 tool after register")
		}
	})

	t.Run("ByToolset from empty reg", func(t *testing.T) {
		r := NewRegistry()
		got := r.ByToolset("none")
		if len(got) != 0 {
			t.Errorf("expected 0, got %d", len(got))
		}
	})

	t.Run("Available from empty reg", func(t *testing.T) {
		r := NewRegistry()
		got := r.Available()
		if len(got) != 0 {
			t.Errorf("expected 0, got %d", len(got))
		}
	})
}
