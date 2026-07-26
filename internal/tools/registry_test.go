package tools

import (
	"errors"
	"slices"
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

func TestRegistryGet(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register(ToolEntry{Name: "hello", Description: "hi", Handler: noopHandler}); err != nil {
			t.Fatalf("register: %v", err)
		}
		e, ok := r.Get("hello")
		if !ok {
			t.Fatal("expected found=true")
		}
		if e.Name != "hello" || e.Description != "hi" {
			t.Errorf("Get returned %+v", e)
		}
	})

	t.Run("not found", func(t *testing.T) {
		r := NewRegistry()
		_, ok := r.Get("nonexistent")
		if ok {
			t.Error("expected found=false for unregistered name")
		}
	})

	t.Run("returned entry is a defensive copy", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register(ToolEntry{Name: "hello", Schema: JSONSchema{"type": "object"}, Handler: noopHandler}); err != nil {
			t.Fatalf("register: %v", err)
		}
		e, _ := r.Get("hello")
		e.Schema["type"] = "mutated"
		e2, _ := r.Get("hello")
		if e2.Schema["type"] != "object" {
			t.Errorf("Get did not defensively copy: registry's schema was mutated to %v", e2.Schema["type"])
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
		_ = r.Register(ToolEntry{Name: "x", Handler: noopHandler})
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
	_ = r.Register(ToolEntry{Name: "f1", Toolset: "file", Handler: noopHandler})
	_ = r.Register(ToolEntry{Name: "f2", Toolset: "file", Handler: noopHandler})
	_ = r.Register(ToolEntry{Name: "g1", Toolset: "git", Handler: noopHandler})
	_ = r.Register(ToolEntry{Name: "u1", Toolset: "", Handler: noopHandler}) // ungrouped

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
	_ = r.Register(ToolEntry{Name: "always", Handler: noopHandler})
	_ = r.Register(ToolEntry{Name: "offline", Handler: noopHandler, CheckFn: func() bool { return false }})
	_ = r.Register(ToolEntry{Name: "conditional", Handler: noopHandler, CheckFn: func() bool { return true }})

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
		_ = r.Register(ToolEntry{
			Name:    "toggle",
			Handler: noopHandler,
			CheckFn: func() bool { return flag },
		})
		if len(r.Available()) != 3 {
			t.Errorf("expected 3 available (toggle is true), got %d", len(r.Available()))
		}
		flag = false
		// Available() re-evaluates  --  the closure captures &flag.
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

import "github.com/samcharles93/archie-core/internal/tools"

func init() {
	tools.DefaultRegistry().Register(tools.ToolEntry{
		Name:        "discovered_hello",
		Description: "A discovered tool",
	})
}
`),
			},
		}
		// Discover finds and counts Register calls. The entry is not
		// actually registered because Handler (a function field) cannot
		// be resolved from source via static AST analysis.
		n, err := r.Discover(fsys, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 1 {
			t.Errorf("expected 1 discovered, got %d", n)
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

	t.Run("multiple Register calls in one file", func(t *testing.T) {
		r := NewRegistry()
		fsys := fstest.MapFS{
			"tools.go": &fstest.MapFile{
				Data: []byte(`package tools

func init() {
	DefaultRegistry().Register(ToolEntry{Name: "tool-a", Description: "First tool"})
	DefaultRegistry().Register(ToolEntry{Name: "tool-b", Description: "Second tool"})
}
`),
			},
		}
		n, err := r.Discover(fsys, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 2 {
			t.Errorf("expected 2 discovered, got %d", n)
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

func TestRegistryAvailableReentrySafety(t *testing.T) {
	// Regression: Available() called CheckFn while holding RLock,
	// which could deadlock if CheckFn re-entered the registry.
	// After the fix, CheckFn is evaluated outside the lock, so
	// calling All(), ByToolset(), or even Register() from inside
	// CheckFn cannot deadlock.
	r := NewRegistry()
	_ = r.Register(ToolEntry{Name: "safe", Handler: noopHandler})

	called := false
	_ = r.Register(ToolEntry{
		Name:    "reentrant",
		Handler: noopHandler,
		CheckFn: func() bool {
			called = true
			// Call All() from inside CheckFn  --  must not deadlock.
			_ = r.All()
			// Call ByToolset from inside CheckFn  --  must not deadlock.
			_ = r.ByToolset("")
			// Call Register from inside CheckFn  --  must not deadlock
			// (was the original deadlock trigger when lock was held).
			_ = r.Register(ToolEntry{Name: "nested-reg", Handler: noopHandler})
			return true
		},
	})

	avail := r.Available()
	if !called {
		t.Error("CheckFn was not called")
	}
	names := toolNames(avail)
	if !slices.Contains(names, "reentrant") {
		t.Error("reentrant tool should be available")
	}
}

func TestRegistryRegisterDefensiveCopy(t *testing.T) {
	// Regression: Register() stored the caller's Schema map and
	// RequiresEnv slice without deep-copying, enabling data races.
	r := NewRegistry()

	schema := JSONSchema{"key": "original"}
	env := []string{"ORIGINAL"}
	e := ToolEntry{Name: "copy-test", Handler: noopHandler, Schema: schema, RequiresEnv: env}

	if err := r.Register(e); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Mutate the original schema and env  --  must not affect registry.
	schema["key"] = "mutated"
	env[0] = "MUTATED"

	all := r.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(all))
	}
	if all[0].Schema["key"] != "original" {
		t.Errorf("Schema[key] = %v, want 'original'  --  caller mutation leaked into registry", all[0].Schema["key"])
	}
	if all[0].RequiresEnv[0] != "ORIGINAL" {
		t.Errorf("RequiresEnv[0] = %q, want 'ORIGINAL'  --  caller mutation leaked into registry", all[0].RequiresEnv[0])
	}
}

func TestRegistryDiscoverContinuesOnError(t *testing.T) {
	// Regression: Discover aborted on first parse error, losing prior
	// registrations. After fix, it continues to remaining files.
	r := NewRegistry()
	fsys := fstest.MapFS{
		"good.go": &fstest.MapFile{
			Data: []byte(`package tools

func init() {
	DefaultRegistry().Register(ToolEntry{Name: "good-tool", Description: "Valid"})
}
`),
		},
		"broken.go": &fstest.MapFile{
			Data: []byte(`package tools

func broken( {`),
		},
		"another.go": &fstest.MapFile{
			Data: []byte(`package tools

func init() {
	DefaultRegistry().Register(ToolEntry{Name: "another-tool", Description: "Also valid"})
}
`),
		},
	}
	n, err := r.Discover(fsys, ".")
	// Should report the parse error.
	if err == nil {
		t.Error("expected error for broken.go")
	}
	// Should still count Register calls from good files.
	if n != 2 {
		t.Errorf("expected 2 discovered (good.go + another.go), got %d", n)
	}
}

func TestRegistryDiscoverNonRegistryRegisterIgnored(t *testing.T) {
	// Regression: isRegisterCall matched ANY .Register() method.
	// After fix, only DefaultRegistry().Register() or bare identifiers
	// are matched.
	r := NewRegistry()
	fsys := fstest.MapFS{
		"not_a_tool.go": &fstest.MapFile{
			Data: []byte(`package tools

type Other struct{}
func (o *Other) Register(name string) {}

func init() {
	var o Other
	o.Register("not-a-tool")
	DefaultRegistry().Register(ToolEntry{Name: "real-tool", Description: "A real tool"})
}
`),
		},
	}
	n, err := r.Discover(fsys, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the DefaultRegistry().Register() call should be counted.
	if n != 1 {
		t.Errorf("expected 1 (only DefaultRegistry call), got %d", n)
	}
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

func TestRegistryRegisterBatchIsAtomic(t *testing.T) {
	tests := []struct {
		name     string
		existing []ToolEntry
		batch    []ToolEntry
		wantErr  bool
		want     []string
	}{
		{
			name: "registers complete batch",
			batch: []ToolEntry{
				{Name: "one", Handler: noopHandler},
				{Name: "two", Handler: noopHandler},
			},
			want: []string{"one", "two"},
		},
		{
			name:     "existing collision registers nothing",
			existing: []ToolEntry{{Name: "taken", Handler: noopHandler}},
			batch: []ToolEntry{
				{Name: "new", Handler: noopHandler},
				{Name: "taken", Handler: noopHandler},
			},
			wantErr: true,
			want:    []string{"taken"},
		},
		{
			name: "duplicate inside batch registers nothing",
			batch: []ToolEntry{
				{Name: "same", Handler: noopHandler},
				{Name: "same", Handler: noopHandler},
			},
			wantErr: true,
		},
		{
			name: "invalid entry registers nothing",
			batch: []ToolEntry{
				{Name: "valid", Handler: noopHandler},
				{Name: "invalid"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			for _, entry := range tt.existing {
				if err := registry.Register(entry); err != nil {
					t.Fatal(err)
				}
			}

			err := registry.RegisterBatch(tt.batch)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RegisterBatch() error = %v, wantErr %t", err, tt.wantErr)
			}
			got := make([]string, 0, len(registry.All()))
			for _, entry := range registry.All() {
				got = append(got, entry.Name)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("registered names = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistryUnregister(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"keep", "remove"} {
		if err := registry.Register(ToolEntry{Name: name, Handler: noopHandler}); err != nil {
			t.Fatal(err)
		}
	}

	registry.Unregister("remove", "unknown")

	if _, ok := registry.Get("remove"); ok {
		t.Fatal("removed tool remains registered")
	}
	if _, ok := registry.Get("keep"); !ok {
		t.Fatal("unrelated tool was removed")
	}
}
