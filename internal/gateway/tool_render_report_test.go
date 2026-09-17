package gateway

import (
	"fmt"
	"strings"
	"testing"
)

// Operator report (2026-09-17): tool output in Telegram is unreadable. A
// grep-heavy turn rendered as a wall of blocks like:
//
//	🔧 grep — done
//	```text
//	/home/sam/.claude/config.json-322-          }
//	… 194 more lines
//	```
//
// Three faults, all in the render path:
//
//  1. the preview is an arbitrary match line -- a path, a line number and a
//     stray brace -- with no statement of what matched or how much;
//  2. the "… N more lines" we append disagrees with the count the tool
//     already printed, so one result set shows two different numbers;
//  3. a grep result ends with the tool's own truncation notice, which the
//     preview renders as if it were a match.

// TestGrepContextPreviewIsNotAnArbitraryMatchLine: the first line of ripgrep
// context output is a path, a line number and (here) a closing brace. Showing
// it tells the operator nothing about the search.
func TestGrepContextPreviewIsNotAnArbitraryMatchLine(t *testing.T) {
	output := strings.Join([]string{
		"/home/sam/.claude/config.json-322-          }",
		"/home/sam/.claude/config.json-323-        }",
		"/home/sam/.claude/config.json-324-      }",
		"",
		"[truncated: showing 3/197 lines, 412B/24.1KB]",
	}, "\n")

	got := RenderToolCall(ToolCallEvent{Name: "grep", Output: output})

	if strings.Contains(got, "config.json-322-") {
		t.Errorf("preview is an arbitrary match line, not a summary:\n%s", got)
	}
	if strings.Contains(got, "[truncated:") {
		t.Errorf("preview quoted the tool's truncation notice as content:\n%s", got)
	}
	// A summary is expected: the operator needs to know how much matched.
	if !strings.Contains(got, "197") {
		t.Errorf("preview does not state the result size; want the tool's own 197 lines:\n%s", got)
	}
}

// TestToolPreviewDoesNotStateItsOwnLineCount: the renderer recomputes a line
// count from the previewed text instead of using the total the tool already
// reported, so the operator sees two different numbers for one result set --
// the tool's "194 more lines" and our "… 3 more lines". Any count we invent
// from a truncated preview is wrong by construction, so the renderer must not
// state one at all.
func TestToolPreviewDoesNotStateItsOwnLineCount(t *testing.T) {
	output := strings.Join([]string{
		"/a/b.go-12-  return nil",
		"/a/c.go-40-  return nil",
		"",
		"[truncated: showing 2/194 lines, 300B/12.0KB]",
	}, "\n")

	got := RenderToolCall(ToolCallEvent{Name: "grep", Output: output})

	if strings.Contains(got, "more lines") {
		t.Errorf("renderer invented a line count from a truncated preview; the tool's own count is the only true one:\n%s", got)
	}
	// The tool's real total should be what survives.
	if !strings.Contains(got, "194") {
		t.Errorf("render lost the tool's real total (194 lines):\n%s", got)
	}
}

// TestToolRenderIsBoundedAndAggregatable: one rendered call must stay small so
// a tool-heavy turn cannot crowd the answer out of a single Telegram frame.
// Bounded at 220 runes so 14 calls stay well inside the 3900-rune frame.
func TestToolRenderIsBoundedAndAggregatable(t *testing.T) {
	output := strings.Repeat("/very/long/path/to/some/file.go-1234-    someLineOfSource();\n", 50)
	got := RenderToolCall(ToolCallEvent{Name: "grep", Output: output})
	if n := len([]rune(got)); n > 220 {
		t.Errorf("one rendered tool call is %d runes, want <= 220:\n%s", n, got)
	}
	// 14 such calls must leave the majority of the frame for the answer.
	if n := len([]rune(got)) * 14; n > 3900/2 {
		t.Errorf("14 rendered calls would consume %d of a 3900-rune frame", n)
	}
}

// TestToolRenderKeepsShortOutputInformative: bounding must not throw away a
// short result that genuinely fits -- the existing behaviour these tests must
// not regress.
func TestToolRenderKeepsShortOutputInformative(t *testing.T) {
	got := RenderToolCall(ToolCallEvent{Name: "shell", Output: "total 8\ndrwxr-xr-x 2 sam sam"})
	if !strings.Contains(got, "total 8") {
		t.Errorf("short output lost its content: %q", got)
	}
}

// TestToolRenderCountsMatchesForSearchTools: a search summary should say how
// many matches, which is the one fact that makes it useful.
func TestToolRenderCountsMatchesForSearchTools(t *testing.T) {
	var b strings.Builder
	for i := range 7 {
		fmt.Fprintf(&b, "/src/file%d.go-%d-  match\n", i, i*10)
	}
	got := RenderToolCall(ToolCallEvent{Name: "grep", Output: b.String()})
	if !strings.Contains(got, "7") {
		t.Errorf("search preview does not report the match count:\n%s", got)
	}
}
