package tools

import (
	"context"
	"strings"
	"testing"
)

func TestIsBridgeTool(t *testing.T) {
	if !IsBridgeTool(BridgeToolSearch) {
		t.Error("tool_search should be a bridge tool")
	}
	if !IsBridgeTool(BridgeToolDescribe) {
		t.Error("tool_describe should be a bridge tool")
	}
	if !IsBridgeTool(BridgeToolCall) {
		t.Error("tool_call should be a bridge tool")
	}
	if IsBridgeTool("ordinary_tool") {
		t.Error("ordinary_tool should not be a bridge tool")
	}
	if IsBridgeTool("") {
		t.Error("empty string should not be a bridge tool")
	}
}

func TestBridgeTools(t *testing.T) {
	r := NewRegistry()
	bt := BridgeTools(r)

	if len(bt) != 3 {
		t.Fatalf("expected 3 bridge tools, got %d", len(bt))
	}

	names := make(map[string]bool)
	for _, e := range bt {
		names[e.Name] = true
		if e.Handler == nil {
			t.Errorf("bridge tool %q has nil handler", e.Name)
		}
		if !e.Classification.IsIdempotent() {
			t.Errorf("bridge tool %q should be idempotent", e.Name)
		}
		if e.Toolset != "bridge" {
			t.Errorf("bridge tool %q has toolset %q, want 'bridge'", e.Name, e.Toolset)
		}
	}

	if !names[BridgeToolSearch] {
		t.Error("missing tool_search")
	}
	if !names[BridgeToolDescribe] {
		t.Error("missing tool_describe")
	}
	if !names[BridgeToolCall] {
		t.Error("missing tool_call")
	}
}

func TestBridgeToolSearch(t *testing.T) {
	r := NewRegistry()

	_ = r.Register(ToolEntry{
		Name:        "read_file",
		Toolset:     "file",
		Description: "Read a file from disk",
		Handler:     noopHandler,
	})
	_ = r.Register(ToolEntry{
		Name:        "write_file",
		Toolset:     "file",
		Description: "Write content to a file",
		Handler:     noopHandler,
	})
	_ = r.Register(ToolEntry{
		Name:        "git_commit",
		Toolset:     "git",
		Description: "Create a git commit",
		Handler:     noopHandler,
	})

	bt := BridgeTools(r)
	var searchTool ToolEntry
	for _, e := range bt {
		if e.Name == BridgeToolSearch {
			searchTool = e
			break
		}
	}

	t.Run("search all", func(t *testing.T) {
		out, err := searchTool.Handler(context.Background(), map[string]any{"keyword": ""})
		if err != nil {
			t.Fatalf("tool_search error: %v", err)
		}
		result := out.(map[string]any)
		tools := result["tools"].([]ToolSummary)
		if len(tools) != 3 {
			t.Errorf("expected 3 tools, got %d", len(tools))
		}
	})

	t.Run("search by keyword", func(t *testing.T) {
		out, err := searchTool.Handler(context.Background(), map[string]any{"keyword": "file"})
		if err != nil {
			t.Fatalf("tool_search error: %v", err)
		}
		result := out.(map[string]any)
		tools := result["tools"].([]ToolSummary)
		if len(tools) != 2 {
			t.Errorf("expected 2 file tools, got %d", len(tools))
		}
	})

	t.Run("search no match", func(t *testing.T) {
		out, err := searchTool.Handler(context.Background(), map[string]any{"keyword": "nonexistent"})
		if err != nil {
			t.Fatalf("tool_search error: %v", err)
		}
		result := out.(map[string]any)
		tools := result["tools"].([]ToolSummary)
		if len(tools) != 0 {
			t.Errorf("expected 0 tools, got %d", len(tools))
		}
	})

	t.Run("bridge tools excluded from results", func(t *testing.T) {
		out, err := searchTool.Handler(context.Background(), map[string]any{"keyword": "tool_"})
		if err != nil {
			t.Fatalf("tool_search error: %v", err)
		}
		result := out.(map[string]any)
		tools := result["tools"].([]ToolSummary)
		for _, ts := range tools {
			if IsBridgeTool(ts.Name) {
				t.Errorf("bridge tool %q should not appear in search results", ts.Name)
			}
		}
	})
}

func TestBridgeToolDescribe(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(ToolEntry{
		Name:        "read_file",
		Toolset:     "file",
		Description: "Read a file from disk",
		Handler:     noopHandler,
		Schema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"path": JSONSchema{"type": "string"},
			},
		},
		RequiresEnv: []string{"HOME"},
	})

	bt := BridgeTools(r)
	var describeTool ToolEntry
	for _, e := range bt {
		if e.Name == BridgeToolDescribe {
			describeTool = e
			break
		}
	}

	t.Run("describe existing tool", func(t *testing.T) {
		out, err := describeTool.Handler(context.Background(), map[string]any{"name": "read_file"})
		if err != nil {
			t.Fatalf("tool_describe error: %v", err)
		}
		result := out.(map[string]any)
		toolMap := result["tool"].(map[string]any)
		if toolMap["name"] != "read_file" {
			t.Errorf("name = %v, want 'read_file'", toolMap["name"])
		}
		if toolMap["description"] != "Read a file from disk" {
			t.Errorf("description = %v", toolMap["description"])
		}
		if toolMap["toolset"] != "file" {
			t.Errorf("toolset = %v, want 'file'", toolMap["toolset"])
		}
		if toolMap["schema"] == nil {
			t.Error("schema should not be nil")
		}
	})

	t.Run("describe missing tool", func(t *testing.T) {
		_, err := describeTool.Handler(context.Background(), map[string]any{"name": "nope"})
		if err == nil {
			t.Error("expected error for missing tool")
		}
	})

	t.Run("describe without name", func(t *testing.T) {
		_, err := describeTool.Handler(context.Background(), map[string]any{})
		if err == nil {
			t.Error("expected error for missing name parameter")
		}
	})
}

func TestBridgeToolCall(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(ToolEntry{
		Name:    "echo",
		Handler: echoHandler,
	})

	bt := BridgeTools(r)
	var callTool ToolEntry
	for _, e := range bt {
		if e.Name == BridgeToolCall {
			callTool = e
			break
		}
	}

	t.Run("call existing tool", func(t *testing.T) {
		out, err := callTool.Handler(context.Background(), map[string]any{
			"name":  "echo",
			"input": map[string]any{"msg": "hello"},
		})
		if err != nil {
			t.Fatalf("tool_call error: %v", err)
		}
		m := out.(map[string]any)
		if m["msg"] != "hello" {
			t.Errorf("msg = %v, want 'hello'", m["msg"])
		}
	})

	t.Run("call missing tool", func(t *testing.T) {
		_, err := callTool.Handler(context.Background(), map[string]any{"name": "nope"})
		if err == nil {
			t.Error("expected error for missing tool")
		}
	})

	t.Run("call without name", func(t *testing.T) {
		_, err := callTool.Handler(context.Background(), map[string]any{})
		if err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("cannot call bridge tool recursively", func(t *testing.T) {
		_, err := callTool.Handler(context.Background(), map[string]any{"name": BridgeToolSearch})
		if err == nil {
			t.Error("expected error when calling bridge tool")
		}
	})
}

func TestBridgeToolsRegistration(t *testing.T) {
	r := NewRegistry()
	for _, e := range BridgeTools(r) {
		if err := r.Register(e); err != nil {
			t.Fatalf("register bridge tool %q: %v", e.Name, err)
		}
	}

	available := r.Available()
	if len(available) != 3 {
		t.Errorf("expected 3 bridge tools registered, got %d", len(available))
	}
}

func TestNewContextPressureGate(t *testing.T) {
	g := NewContextPressureGate(100_000)

	if g.ThresholdFraction != 0.5 {
		t.Errorf("ThresholdFraction = %v, want 0.5", g.ThresholdFraction)
	}
	if g.ContextWindowSize != 100_000 {
		t.Errorf("ContextWindowSize = %d, want 100000", g.ContextWindowSize)
	}
	if g.Mode() != DisclosureFull {
		t.Error("initial mode should be full disclosure")
	}
}

func TestContextPressureGateDisabled(t *testing.T) {
	g := NewContextPressureGate(0)
	g.Evaluate(nil)
	if g.Mode() != DisclosureFull {
		t.Error("disabled gate should always be full disclosure")
	}
}

func TestContextPressureGateSwitchesToBridge(t *testing.T) {
	g := NewContextPressureGate(1000)

	tools := []ToolEntry{
		{
			Name:        "big-tool",
			Description: strings.Repeat("x", 600),
			Handler:     noopHandler,
		},
	}

	g.Evaluate(tools)
	if g.Mode() != DisclosureBridge {
		t.Error("large schemas should trigger bridge mode")
	}
}

func TestContextPressureGateStaysFull(t *testing.T) {
	g := NewContextPressureGate(100_000)

	tools := []ToolEntry{
		{
			Name:        "small-tool",
			Description: "A simple tool",
			Handler:     noopHandler,
		},
	}

	g.Evaluate(tools)
	if g.Mode() != DisclosureFull {
		t.Error("small schemas should keep full disclosure")
	}
}

func TestContextPressureGateAlwaysVisible(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(ToolEntry{Name: "essential", Handler: noopHandler, Description: "Always needed"})
	_ = r.Register(ToolEntry{Name: "optional", Handler: noopHandler, Description: strings.Repeat("x", 600)})

	for _, e := range BridgeTools(r) {
		_ = r.Register(e)
	}

	g := NewContextPressureGate(1000)
	g.SetAlwaysVisible("essential")

	g.Evaluate(r.All())

	if g.Mode() != DisclosureBridge {
		t.Fatal("expected bridge mode")
	}

	filtered := g.FilterTools(r)
	names := make(map[string]bool)
	for _, e := range filtered {
		names[e.Name] = true
	}

	if !names["essential"] {
		t.Error("essential tool should be always visible")
	}
	if !names[BridgeToolSearch] {
		t.Error("bridge tools should be visible in bridge mode")
	}
	if names["optional"] {
		t.Error("optional tool should not be visible in bridge mode")
	}
}

func TestContextPressureGateReset(t *testing.T) {
	g := NewContextPressureGate(100)
	tools := []ToolEntry{
		{Name: "big", Description: strings.Repeat("x", 200), Handler: noopHandler},
	}

	g.Evaluate(tools)
	if g.Mode() != DisclosureBridge {
		t.Fatal("expected bridge mode")
	}

	g.Reset()
	if g.Mode() != DisclosureFull {
		t.Error("after reset, should be full disclosure")
	}
}

func TestDisclosureModeString(t *testing.T) {
	if DisclosureFull.String() != "full" {
		t.Errorf("DisclosureFull = %q", DisclosureFull.String())
	}
	if DisclosureBridge.String() != "bridge" {
		t.Errorf("DisclosureBridge = %q", DisclosureBridge.String())
	}
	if DisclosureMode(99).String() != "unknown" {
		t.Errorf("unknown mode = %q", DisclosureMode(99).String())
	}
}

func TestMatchesKeyword(t *testing.T) {
	e := ToolEntry{
		Name:        "read_file",
		Toolset:     "file",
		Description: "Read a file from disk",
	}

	if !matchesKeyword(e, "file") {
		t.Error("should match by name")
	}
	if !matchesKeyword(e, "disk") {
		t.Error("should match by description")
	}
	if !matchesKeyword(e, "FILE") {
		t.Error("should match case-insensitive")
	}
	if matchesKeyword(e, "nonexistent") {
		t.Error("should not match")
	}
}

func TestTotalSchemaSize(t *testing.T) {
	tools := []ToolEntry{
		{
			Name:        "tool-a",
			Description: "desc a",
			Schema: JSONSchema{
				"type":       "object",
				"properties": JSONSchema{"x": JSONSchema{"type": "string"}},
			},
		},
	}

	size := totalSchemaSize(tools)
	if size <= 0 {
		t.Error("schema size should be non-zero")
	}
}
