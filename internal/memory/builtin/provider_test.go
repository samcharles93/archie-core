package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestProvider_Identity(t *testing.T) {
	p := newTestProvider(t)
	if p.Name() != "builtin" {
		t.Fatalf("expected name builtin, got %q", p.Name())
	}
	if !p.IsAvailable() {
		t.Fatalf("expected builtin provider to always be available")
	}
	if err := p.Initialize("session-1"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestProvider_Initialize_ReloadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	p, err := New(Config{Dir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("## Facts\n\nexternal entry\n"), 0o644); err != nil {
		t.Fatalf("writing external content: %v", err)
	}

	if err := p.Initialize("session-1"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !strings.Contains(p.memory.Render(), "external entry") {
		t.Fatalf("expected Initialize to reload MEMORY.md, got %q", p.memory.Render())
	}
}

func TestProvider_New_RequiresDir(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatalf("expected error for empty Dir")
	}
}

func TestProvider_New_CreatesDirAndFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "memory")
	p, err := New(Config{Dir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.HandleToolCall(toolName, map[string]any{
		"action":  "add",
		"section": "Facts",
		"content": "hello",
	}); err != nil {
		t.Fatalf("HandleToolCall add: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "MEMORY.md")); statErr != nil {
		t.Fatalf("expected MEMORY.md to exist: %v", statErr)
	}
}

// TestProvider_GetToolSchemas checks the exported schema shape, and that
// Handler is left nil for the Manager to inject via ToolCallProvider (per
// Manager.GetToolSchemas' documented behavior).
func TestProvider_GetToolSchemas(t *testing.T) {
	p := newTestProvider(t)
	entries := p.GetToolSchemas()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one tool entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != toolName {
		t.Fatalf("expected tool name %q, got %q", toolName, e.Name)
	}
	if e.Handler != nil {
		t.Fatalf("expected nil Handler so Manager injects the ToolCallProvider delegate")
	}

	// Simulate the Manager's injection and confirm the resulting entry
	// passes Validate().
	e = e.Clone()
	e.Handler = func(ctx context.Context, input map[string]any) (any, error) {
		return p.HandleToolCall(e.Name, input)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("ToolEntry.Validate() after injection: %v", err)
	}
}

func TestProvider_HandleToolCall_UnknownTool(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.HandleToolCall("not_memory_edit", map[string]any{}); err == nil {
		t.Fatalf("expected error for unknown tool name")
	}
}

func TestProvider_HandleToolCall_Add_DefaultsToMemoryFile(t *testing.T) {
	p := newTestProvider(t)
	out, err := p.HandleToolCall(toolName, map[string]any{
		"action":  "add",
		"section": "Facts",
		"content": "the sky is blue",
	})
	if err != nil {
		t.Fatalf("HandleToolCall: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any result, got %T", out)
	}
	if result["file"] != "memory" {
		t.Fatalf("expected default file \"memory\", got %v", result["file"])
	}
	if !strings.Contains(p.memory.Render(), "the sky is blue") {
		t.Fatalf("expected content written to MEMORY.md, got %q", p.memory.Render())
	}
	if strings.Contains(p.user.Render(), "the sky is blue") {
		t.Fatalf("did not expect content in USER.md")
	}
}

func TestProvider_HandleToolCall_Add_UserFile(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.HandleToolCall(toolName, map[string]any{
		"file":    "user",
		"action":  "add",
		"section": "Preferences",
		"content": "likes dark mode",
	})
	if err != nil {
		t.Fatalf("HandleToolCall: %v", err)
	}
	if !strings.Contains(p.user.Render(), "likes dark mode") {
		t.Fatalf("expected content written to USER.md, got %q", p.user.Render())
	}
}

func TestProvider_HandleToolCall_UnknownFile(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.HandleToolCall(toolName, map[string]any{
		"file":    "bogus",
		"action":  "add",
		"section": "Facts",
		"content": "x",
	})
	if err == nil {
		t.Fatalf("expected error for unknown file selector")
	}
}

func TestProvider_HandleToolCall_UnknownAction(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.HandleToolCall(toolName, map[string]any{
		"action":  "delete",
		"section": "Facts",
	})
	if err == nil {
		t.Fatalf("expected error for unknown action")
	}
}

func TestProvider_HandleToolCall_Replace(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.HandleToolCall(toolName, map[string]any{
		"action": "add", "section": "Facts", "content": "the sky is blue",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := p.HandleToolCall(toolName, map[string]any{
		"action": "replace", "section": "Facts", "substring": "blue", "replacement": "green",
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	result := out.(map[string]any)
	if result["action"] != "replace" {
		t.Fatalf("expected action replace, got %v", result["action"])
	}
	if !strings.Contains(p.memory.Render(), "the sky is green") {
		t.Fatalf("expected replacement applied, got %q", p.memory.Render())
	}
}

func TestProvider_HandleToolCall_Remove(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.HandleToolCall(toolName, map[string]any{
		"action": "add", "section": "Facts", "content": "entry one",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := p.HandleToolCall(toolName, map[string]any{
		"action": "remove", "section": "Facts", "substring": "entry one",
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	result := out.(map[string]any)
	if result["action"] != "remove" {
		t.Fatalf("expected action remove, got %v", result["action"])
	}
	if strings.Contains(p.memory.Render(), "entry one") {
		t.Fatalf("expected entry removed, got %q", p.memory.Render())
	}
}

func TestProvider_HandleToolCall_Add_RequiresContent(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.HandleToolCall(toolName, map[string]any{
		"action": "add", "section": "Facts",
	}); err == nil {
		t.Fatalf("expected error when content is missing")
	}
}

func TestProvider_HandleToolCall_Replace_RequiresSubstring(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.HandleToolCall(toolName, map[string]any{
		"action": "replace", "section": "Facts", "replacement": "x",
	}); err == nil {
		t.Fatalf("expected error when substring is missing")
	}
}

func TestProvider_HandleToolCall_InvalidSectionName(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.HandleToolCall(toolName, map[string]any{
		"action": "add", "section": "bad-section!", "content": "x",
	}); err == nil {
		t.Fatalf("expected error for invalid section name")
	}
}

// TestProvider_ConcurrentToolCalls exercises the store's mutex under
// concurrent add calls from multiple goroutines, simulating concurrent
// tool invocations against the same provider.
func TestProvider_ConcurrentToolCalls(t *testing.T) {
	p := newTestProvider(t)
	const n = 50

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := p.HandleToolCall(toolName, map[string]any{
				"action":  "add",
				"section": "Facts",
				"content": "entry " + strconv.Itoa(i),
			})
			if err != nil {
				t.Errorf("concurrent add %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	rendered := p.memory.Render()
	if strings.Count(rendered, "## Facts") != 1 {
		t.Fatalf("expected exactly one Facts header after concurrent writes, got: %q", rendered)
	}
}
