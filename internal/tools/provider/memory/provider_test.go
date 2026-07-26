package memory

import (
	"context"
	"testing"

	corememory "github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
)

func TestProviderDiscoversExecutableMemoryTools(t *testing.T) {
	backend := &fakeMemoryProvider{
		available: true,
		entries: []tools.ToolEntry{{
			Name:        "memory_search",
			Description: "Search memory",
			Schema:      tools.JSONSchema{"type": "object"},
		}},
		result: "remembered",
	}
	manager, err := corememory.NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Shutdown(); err != nil {
			t.Errorf("memory manager shutdown: %v", err)
		}
	})
	provider := New(manager)

	if err := provider.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "memory_search" {
		t.Fatalf("Discover() = %v, want memory_search", entryNames(entries))
	}
	if entries[0].Handler == nil {
		t.Fatal("memory tool handler is nil")
	}
	got, err := entries[0].Handler(context.Background(), map[string]any{"query": "architecture"})
	if err != nil {
		t.Fatal(err)
	}
	if got != backend.result {
		t.Fatalf("handler result = %v, want %v", got, backend.result)
	}
	if backend.calledName != "memory_search" || backend.calledArgs["query"] != "architecture" {
		t.Fatalf("memory call = %q %#v", backend.calledName, backend.calledArgs)
	}
}

func TestProviderLifecycleWithNilManager(t *testing.T) {
	provider := New(nil)
	if err := provider.Start(context.Background()); err == nil {
		t.Fatal("Start() succeeded with nil manager")
	}
	if _, err := provider.Discover(context.Background()); err == nil {
		t.Fatal("Discover() succeeded with nil manager")
	}
	if got := provider.Health(context.Background()).Status; got != plugin.HealthUnhealthy {
		t.Fatalf("Health().Status = %q, want unhealthy", got)
	}
	if err := provider.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() = %v, want no-op", err)
	}
}

func TestProviderSatisfiesToolProviderEngine(t *testing.T) {
	var _ interface {
		plugin.Module
		Discover(context.Context) ([]tools.ToolEntry, error)
	} = New(nil)
}

type fakeMemoryProvider struct {
	available  bool
	entries    []tools.ToolEntry
	result     any
	callErr    error
	calledName string
	calledArgs map[string]any
}

func (p *fakeMemoryProvider) Name() string {
	return "fake-memory"
}

func (p *fakeMemoryProvider) IsAvailable() bool {
	return p.available
}

func (p *fakeMemoryProvider) Initialize(string) error {
	return nil
}

func (p *fakeMemoryProvider) GetToolSchemas() []tools.ToolEntry {
	return p.entries
}

func (p *fakeMemoryProvider) HandleToolCall(name string, args map[string]any) (any, error) {
	p.calledName = name
	p.calledArgs = args
	if p.callErr != nil {
		return nil, p.callErr
	}
	return p.result, nil
}

func entryNames(entries []tools.ToolEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

var _ corememory.ToolCallProvider = (*fakeMemoryProvider)(nil)
