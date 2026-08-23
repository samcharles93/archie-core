package gateway

import (
	"strings"
	"testing"
)

func TestToolCallEventRenderToolCallUsesCompactSafeOutput(t *testing.T) {
	got := (ToolCallEvent{Name: "shell", Parameters: `{"command":"{schema placeholder}"}`, Output: "line one\nline two"}).RenderToolCall()
	if strings.Contains(got, "schema placeholder") || strings.Contains(got, "Parameters") {
		t.Fatalf("render leaked input/schema noise: %q", got)
	}
	want := "🔧 shell\n```text\nline one\nline two\n```"
	if got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
}

func TestToolCallEventRenderToolCallBoundsOutputAndPreservesFailure(t *testing.T) {
	got := (ToolCallEvent{Name: "read", Output: strings.Repeat("x", 2000)}).RenderToolCall()
	if len([]rune(got)) > 520 {
		t.Fatalf("render has %d runes, want bounded output", len([]rune(got)))
	}
	if !strings.Contains(got, "…") {
		t.Fatal("bounded output lacks truncation marker")
	}
	failed := (ToolCallEvent{Name: "shell", Output: "ignored", Err: "command_exit: exit status 2"}).RenderToolCall()
	if failed != "🔧 shell — ❌ command_exit: exit status 2" {
		t.Fatalf("failure render = %q", failed)
	}
}
