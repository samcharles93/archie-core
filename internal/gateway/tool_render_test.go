package gateway

import (
	"strings"
	"testing"
)

func TestToolCallEventRenderToolCallUsesCompactSafeOutput(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "shell", Parameters: `{"command":"{schema placeholder}"}`, Output: "line one\nline two"})
	if strings.Contains(got, "schema placeholder") || strings.Contains(got, "Parameters") {
		t.Fatalf("render leaked input/schema noise: %q", got)
	}
	if !strings.Contains(got, "line one") || strings.Contains(got, "hidden") {
		t.Fatalf("short shell output should be previewed, got %q", got)
	}
}

func TestToolCallEventRenderToolCallShowsUsefulPreviewWithoutDumpingSource(t *testing.T) {
	source := "package agentexec\n\nimport (\n	\"context\"\n)\n\ntype AgentRequestMessage struct {\n	Workflow string\n}\n"
	got := RenderToolCall(ToolCallEvent{Name: "read", Output: source})
	if strings.Contains(got, "type AgentRequestMessage") || strings.Contains(got, "content hidden") {
		t.Fatalf("read preview should show a short lead-in, not dump or hide everything: %q", got)
	}
	if !strings.Contains(got, "package agentexec") {
		t.Fatalf("read preview missing first useful line: %q", got)
	}

	listing := "/home/sam/projects/archie-core\n/home/sam/projects/archie-core/internal/gateway\n/home/sam/go/pkg/mod/github.com/samcharles93/ai-sdk@v0.1.21/prompt/prompt.go\n"
	got = RenderToolCall(ToolCallEvent{Name: "find", Output: listing})
	if strings.Contains(got, "listing hidden") || strings.Contains(got, "/pkg/mod/") {
		t.Fatalf("find preview should show a short local path, not hide or dump module cache: %q", got)
	}
	if !strings.Contains(got, "/home/sam/projects/archie-core") {
		t.Fatalf("find preview missing first path: %q", got)
	}
}

func TestToolCallEventRenderToolCallBoundsOutputAndPreservesFailure(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "read", Output: strings.Repeat("x", 2000)})
	if len([]rune(got)) > 520 {
		t.Fatalf("render has %d runes, want bounded output", len([]rune(got)))
	}
	if strings.Contains(got, strings.Repeat("x", 200)) {
		t.Fatalf("oversized contents were not bounded: %q", got)
	}
	failed := RenderToolCall(ToolCallEvent{Name: "shell", Output: "ignored", Err: "command_exit: exit status 2"})
	if failed != "🔧 shell — failed\n```text\ncommand_exit: exit status 2\n```" {
		t.Fatalf("failure render = %q", failed)
	}
}

func TestToolCallEventFailureKeyCollapsesLegacyTurnBudget(t *testing.T) {
	first := ToolCallEvent{Name: "read", Err: "tool read: turn budget exceeded (200000 chars)"}
	second := ToolCallEvent{Name: "shell", Err: "tool shell: turn budget exceeded (200000 chars)"}
	if FailureKey(first) == "" || FailureKey(first) != FailureKey(second) {
		t.Fatalf("legacy output-limit failures should share one key: %q vs %q", FailureKey(first), FailureKey(second))
	}
	if RenderToolCall(first) != "🔧 tools — stopped\n```text\ntool-output limit reached (200000 chars); further results suppressed\n```" {
		t.Fatalf("legacy output-limit render = %q", RenderToolCall(first))
	}
}
