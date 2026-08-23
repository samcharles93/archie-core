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
	want := "🔧 shell — done\n```text\ncommand completed; 2 output lines hidden\n```"
	if got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
}

func TestToolCallEventRenderToolCallBoundsOutputAndPreservesFailure(t *testing.T) {
	got := (ToolCallEvent{Name: "read", Output: strings.Repeat("x", 2000)}).RenderToolCall()
	if len([]rune(got)) > 520 {
		t.Fatalf("render has %d runes, want bounded output", len([]rune(got)))
	}
	if strings.Contains(got, strings.Repeat("x", 20)) || !strings.Contains(got, "content hidden") {
		t.Fatalf("file contents were not summarized: %q", got)
	}
	failed := (ToolCallEvent{Name: "shell", Output: "ignored", Err: "command_exit: exit status 2"}).RenderToolCall()
	if failed != "🔧 shell — failed\n```text\ncommand_exit: exit status 2\n```" {
		t.Fatalf("failure render = %q", failed)
	}
}

func TestToolCallEventFailureKeyCollapsesLegacyTurnBudget(t *testing.T) {
	first := ToolCallEvent{Name: "read", Err: "tool read: turn budget exceeded (200000 chars)"}
	second := ToolCallEvent{Name: "shell", Err: "tool shell: turn budget exceeded (200000 chars)"}
	if first.FailureKey() == "" || first.FailureKey() != second.FailureKey() {
		t.Fatalf("legacy output-limit failures should share one key: %q vs %q", first.FailureKey(), second.FailureKey())
	}
	if first.RenderToolCall() != "🔧 tools — stopped\n```text\ntool-output limit reached (200000 chars); further results suppressed\n```" {
		t.Fatalf("legacy output-limit render = %q", first.RenderToolCall())
	}
}
