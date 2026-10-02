package provider

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
)

// startRunningRegistry builds a registry over a fresh tool index with the
// named engines registered and started as one family, the state a live
// reconciliation runs against. It returns both so a test can read the index.
func startRunningRegistry(t *testing.T, engines ...Engine) (*Registry, *tools.Registry) {
	t.Helper()
	index := tools.NewRegistry()
	registry := NewRegistry(index)
	for _, engine := range engines {
		if err := registry.RegisterOptional(engine); err != nil {
			t.Fatalf("RegisterOptional: %v", err)
		}
	}
	if err := registry.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return registry, index
}

func TestRegistryAddStartsOneProviderAlone(t *testing.T) {
	first := newFakeEngine("first")
	first.tools = []tools.ToolEntry{testTool("first.tool")}
	registry, index := startRunningRegistry(t, first)

	second := newFakeEngine("second")
	second.tools = []tools.ToolEntry{testTool("second.tool")}
	if err := registry.Add(t.Context(), second); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if first.startCount != 1 || first.stopCount != 0 {
		t.Errorf("the running provider was disturbed: start=%d stop=%d", first.startCount, first.stopCount)
	}
	if second.startCount != 1 || second.discoverCount != 1 {
		t.Errorf("the added provider was not started: start=%d discover=%d", second.startCount, second.discoverCount)
	}
	if got := registry.RunningIDs(); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("RunningIDs() = %v, want [first second]", got)
	}
	if _, ok := index.Get("second.tool"); !ok {
		t.Error("the added provider's tool was not indexed")
	}
}

func TestRegistryAddIsIsolatedWhenTheProviderFails(t *testing.T) {
	first := newFakeEngine("first")
	first.tools = []tools.ToolEntry{testTool("first.tool")}
	registry, _ := startRunningRegistry(t, first)

	failing := newFakeEngine("second")
	failing.startErr = errors.New("boom")
	if err := registry.Add(t.Context(), failing); err == nil {
		t.Fatal("Add of a provider that cannot start: expected an error")
	}

	if first.startCount != 1 || first.stopCount != 0 {
		t.Errorf("a failed addition rolled the family back: start=%d stop=%d", first.startCount, first.stopCount)
	}
	if _, ok := registry.Get("second"); ok {
		t.Error("the failed provider stayed registered")
	}
	if got := registry.RunningIDs(); !slices.Equal(got, []string{"first"}) {
		t.Errorf("RunningIDs() = %v, want the family unchanged", got)
	}
	if failing.stopCount == 0 {
		t.Error("the half-started provider was not cleaned up")
	}
}

func TestRegistryAddBeforeStartRegistersForTheFamily(t *testing.T) {
	registry := NewRegistry(tools.NewRegistry())
	engine := newFakeEngine("late")
	if err := registry.Add(t.Context(), engine); err != nil {
		t.Fatalf("Add before Start: %v", err)
	}
	if registry.Running() {
		t.Error("Add before Start started the family")
	}
	if engine.startCount != 0 {
		t.Errorf("Add before Start started the engine %d times, want 0", engine.startCount)
	}
	if err := registry.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if engine.startCount != 1 {
		t.Errorf("Start did not pick up the added provider: start=%d", engine.startCount)
	}
}

func TestRegistryRemoveStopsOneProviderAlone(t *testing.T) {
	first := newFakeEngine("first")
	first.tools = []tools.ToolEntry{testTool("first.tool")}
	second := newFakeEngine("second")
	second.tools = []tools.ToolEntry{testTool("second.tool")}
	registry, index := startRunningRegistry(t, first, second)

	if err := registry.Remove(t.Context(), "first"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if first.stopCount != 1 {
		t.Errorf("the removed provider was not stopped: stop=%d", first.stopCount)
	}
	if second.stopCount != 0 || second.startCount != 1 {
		t.Errorf("the surviving provider was disturbed: start=%d stop=%d", second.startCount, second.stopCount)
	}
	if _, ok := registry.Get("first"); ok {
		t.Error("the removed provider stayed registered")
	}
	if _, ok := index.Get("first.tool"); ok {
		t.Error("the removed provider's tool stayed indexed")
	}
	if _, ok := index.Get("second.tool"); !ok {
		t.Error("the surviving provider's tool left the index")
	}
}

func TestRegistryRemoveRejectsUnknownProvider(t *testing.T) {
	registry, _ := startRunningRegistry(t)
	if err := registry.Remove(t.Context(), "missing"); err == nil {
		t.Fatal("Remove of an unregistered provider: expected an error")
	}
}

func TestRegistryReplaceSwapsTheRunningProvider(t *testing.T) {
	previous := newFakeEngine("mcp.alpha")
	previous.tools = []tools.ToolEntry{testTool("alpha.old")}
	registry, index := startRunningRegistry(t, previous)

	replacement := newFakeEngine("mcp.alpha")
	replacement.tools = []tools.ToolEntry{testTool("alpha.new")}
	if err := registry.Replace(t.Context(), replacement); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if previous.stopCount != 1 {
		t.Errorf("the replaced provider was not stopped: stop=%d", previous.stopCount)
	}
	if replacement.startCount != 1 {
		t.Errorf("the replacement was not started: start=%d", replacement.startCount)
	}
	if _, ok := index.Get("alpha.old"); ok {
		t.Error("the replaced provider's tool stayed indexed")
	}
	if _, ok := index.Get("alpha.new"); !ok {
		t.Error("the replacement's tool was not indexed")
	}
}

func TestRegistryReplaceRestoresThePreviousProviderOnFailure(t *testing.T) {
	previous := newFakeEngine("mcp.alpha")
	previous.tools = []tools.ToolEntry{testTool("alpha.old")}
	registry, index := startRunningRegistry(t, previous)

	replacement := newFakeEngine("mcp.alpha")
	replacement.startErr = errors.New("boom")
	err := registry.Replace(t.Context(), replacement)
	if err == nil {
		t.Fatal("Replace with a provider that cannot start: expected an error")
	}

	if previous.stopCount != 1 {
		t.Errorf("the previous provider was not stopped for the replacement: stop=%d", previous.stopCount)
	}
	if previous.startCount != 2 {
		t.Errorf("the previous provider was not restarted: start=%d, want 2", previous.startCount)
	}
	if _, ok := index.Get("alpha.old"); !ok {
		t.Error("the refused replacement left the previous provider's tool out of the index")
	}
	if got := registry.RunningIDs(); !slices.Equal(got, []string{"mcp.alpha"}) {
		t.Errorf("RunningIDs() = %v, want the previous provider still running", got)
	}
}

func TestRegistryReplaceBeforeStartSwapsTheRegistration(t *testing.T) {
	registry := NewRegistry(tools.NewRegistry())
	previous := newFakeEngine("mcp.alpha")
	if err := registry.RegisterOptional(previous); err != nil {
		t.Fatalf("RegisterOptional: %v", err)
	}
	replacement := newFakeEngine("mcp.alpha")
	if err := registry.Replace(t.Context(), replacement); err != nil {
		t.Fatalf("Replace before Start: %v", err)
	}
	if err := registry.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if replacement.startCount != 1 || previous.startCount != 0 {
		t.Errorf("Start ran the wrong registration: replacement=%d previous=%d", replacement.startCount, previous.startCount)
	}
}

func TestRegistryRunningStateReads(t *testing.T) {
	registry := NewRegistry(tools.NewRegistry())
	engine := newFakeEngine("alpha")
	if err := registry.RegisterOptional(engine); err != nil {
		t.Fatalf("RegisterOptional: %v", err)
	}
	if registry.Running() {
		t.Error("Running() = true before Start")
	}
	if !registry.Has("alpha") {
		t.Error("Has(alpha) = false after registration")
	}
	if got := registry.RegisteredIDs(); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("RegisteredIDs() = %v, want [alpha]", got)
	}
	if got := registry.RunningIDs(); len(got) != 0 {
		t.Errorf("RunningIDs() = %v before Start, want none", got)
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !registry.Running() {
		t.Error("Running() = false after Start")
	}
	if got := registry.RunningIDs(); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("RunningIDs() = %v, want [alpha]", got)
	}
}
