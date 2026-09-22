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
	if failed != "shell — failed · command_exit: exit status 2" {
		t.Fatalf("failure render = %q", failed)
	}
}

func TestToolCallEventSuccessKeysCollapseEquivalentResultsWithoutColliding(t *testing.T) {
	equivalent := ToolCallEvent{Name: "grep", Output: "/src/a.go:12:match\n/src/b.go:40:match"}
	equivalentCopy := ToolCallEvent{Name: "grep", Output: "/src/a.go:99:different details\n/src/b.go:7:another detail"}
	differentSummary := ToolCallEvent{Name: "grep", Output: "/src/a.go:12:match"}
	differentTool := ToolCallEvent{Name: "rg", Output: equivalent.Output}

	if key := FailureKey(equivalent); key == "" {
		t.Fatal("successful tool calls need a non-empty aggregation key")
	} else if key != FailureKey(equivalentCopy) {
		t.Fatalf("equivalent successful results should share a key: %q vs %q", key, FailureKey(equivalentCopy))
	}
	if FailureKey(equivalent) == FailureKey(differentSummary) {
		t.Fatalf("different successful summaries must not share a key: %q", FailureKey(equivalent))
	}
	if FailureKey(equivalent) == FailureKey(differentTool) {
		t.Fatalf("different successful tools must not share a key: %q", FailureKey(equivalent))
	}
}

func TestToolCallEventFailureKeyCollapsesLegacyTurnBudget(t *testing.T) {
	first := ToolCallEvent{Name: "read", Err: "tool read: turn budget exceeded (200000 chars)"}
	second := ToolCallEvent{Name: "shell", Err: "tool shell: turn budget exceeded (200000 chars)"}
	if FailureKey(first) == "" || FailureKey(first) != FailureKey(second) {
		t.Fatalf("legacy output-limit failures should share one key: %q vs %q", FailureKey(first), FailureKey(second))
	}
	if RenderToolCall(first) != "tools — stopped · tool-output limit reached (200000 chars); further results suppressed" {
		t.Fatalf("legacy output-limit render = %q", RenderToolCall(first))
	}
}

// TestRenderToolCallKeepsAOneLinePreviewOnItsOwnLine pins the shape that
// decides how much vertical space a turn's tool activity costs.
//
// Telegram renders a fenced block as a full code widget: a language header
// bar, a copy button and a panel. The block parser also inserts a spacer
// paragraph before every block, so a fenced one-line preview cost four
// rendered elements (header, spacer, panel, spacer) to display a word count.
// A single-line preview belongs on the header line.
func TestRenderToolCallKeepsAOneLinePreviewOnItsOwnLine(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "shell", Output: "63"})
	if want := "shell · 63"; got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
	if strings.Contains(got, "```") {
		t.Errorf("a one-line preview must not open a code block: %q", got)
	}
}

// TestRenderToolCallCollapsesMultiLineOutputToOneLine pins that the preview
// is always a single line, whatever the tool printed. Every toolPreview path
// already summarises to one line, which is why the fence it used to sit in
// was never wrapping more than one.
func TestRenderToolCallCollapsesMultiLineOutputToOneLine(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "shell", Output: `{"content":"alpha\nbeta\ngamma"}`})
	if strings.Contains(got, "\n") {
		t.Fatalf("render spans several lines: %q", got)
	}
	if !strings.Contains(got, "alpha") {
		t.Errorf("render dropped the informative line: %q", got)
	}
}

// TestRenderToolCallNeverOpensACodeBlock pins that output carrying its own
// fence cannot start one. The preview follows the header on the same line, so
// a fence marker can never reach column zero -- which is what lets the render
// pass the output through verbatim instead of substituting the backticks away
// and corrupting it.
func TestRenderToolCallNeverOpensACodeBlock(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "read", Output: "```go\nx := 1\n```"})
	if strings.HasPrefix(got, "```") || strings.Contains(got, "\n```") {
		t.Fatalf("render opens a code block: %q", got)
	}
	if !strings.Contains(got, "```go") {
		t.Errorf("backticks were corrupted rather than passed through: %q", got)
	}
}

// TestRenderToolCallUsesTheToolsOwnIcon pins that the icon is data carried on
// the event, not a constant in the renderer. One icon for every tool
// distinguishes nothing, and a renderer that invents one cannot be corrected
// by registering a tool.
func TestRenderToolCallUsesTheToolsOwnIcon(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "shell", Emoji: "💻", Output: "63"})
	if want := "💻 shell · 63"; got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
	failed := RenderToolCall(ToolCallEvent{Name: "shell", Emoji: "💻", Err: "command_exit: exit status 2"})
	if want := "💻 shell — failed · command_exit: exit status 2"; failed != want {
		t.Fatalf("failure render = %q, want %q", failed, want)
	}
}

// TestRenderToolCallOmitsTheIconWhenTheToolDeclaresNone pins the fallback: a
// tool with no icon costs no column, rather than borrowing a generic one that
// says nothing about which tool ran.
func TestRenderToolCallOmitsTheIconWhenTheToolDeclaresNone(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "mcp.github.search", Output: "63"})
	if want := "mcp.github.search · 63"; got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
}
