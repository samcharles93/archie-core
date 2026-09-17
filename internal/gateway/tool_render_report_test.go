package gateway

import (
	"strings"
	"testing"
)

// Operator report (2026-09-17): tool output in the Telegram channel was
// unreadable. A grep-heavy turn rendered as a wall of fenced blocks, each
// quoting one arbitrary match line under a heading that said only
// "grep — done":
//
//	🔧 grep — done
//	/path/config.json-322-          }
//	… 194 more lines
//
// One call rendered as 86 runes carrying no usable information: a path, a line
// number, and a stray closing brace. The renderer now describes the result
// instead of quoting it, and never states a count it computed itself.
//
// Cases are table-driven per the development protocol in AGENTS.md.
func TestToolPreviewDescribesTheResult(t *testing.T) {
	// A 197-line grep result whose first visible line is an arbitrary match;
	// the tool's own notice is the only trustworthy total.
	truncatedGrep := strings.Join([]string{
		"/home/example/.config/app.json-322-          }",
		"/home/example/.config/app.json-323-        }",
		"/home/example/.config/app.json-324-      }",
		"",
		"[truncated: showing 3/197 lines, 412B/24.1KB]",
	}, "\n")

	tests := []struct {
		name   string
		tool   string
		output string
		want   string
	}{
		{
			name:   "truncated search reports the tool's total, which is a head truncation",
			tool:   "grep",
			output: truncatedGrep,
			want:   "197 lines total; showing 3",
		},
		{
			name: "shell truncation notice says 'showing last' and is still metadata, not content",
			tool: "shell",
			output: strings.Join([]string{
				"[truncated: showing last 66/97 lines, 46.2KB/164.8KB]",
				"",
				"the last line of the output",
			}, "\n"),
			want: "97 lines total; showing last 66",
		},
		{
			name:   "an untruncated search is counted, not sampled",
			tool:   "grep",
			output: "/src/a.go:12:func main() {\n/src/b.go:40:\treturn nil\n/src/c.go:99:}",
			want:   "3 matches",
		},
		{
			name: "search context lines are not matches",
			tool: "grep",
			output: strings.Join([]string{
				"/src/a.go:12:func main() {",
				"/src/a.go-13-\treturn nil",
				"/src/a.go-14-}",
				"--",
				"/src/b.go:40:\treturn nil",
			}, "\n"),
			want: "2 matches",
		},
		{
			name:   "a single match is singular",
			tool:   "grep",
			output: "/src/a.go:12:func main() {",
			want:   "1 match",
		},
		{
			name:   "a colon in the path does not hide the match",
			tool:   "grep",
			output: "/src/weird:dir/a.go:12:func main() {",
			want:   "1 match",
		},
		{
			// A compiler diagnostic has the same "file:line:text" shape as a
			// search hit. Counting it would report a build error as "N matches"
			// and hide the diagnostic the operator actually needs.
			name:   "a compiler diagnostic is shown, not counted as matches",
			tool:   "shell",
			output: "internal/webui/api_tasks.go:42: undefined: forgeLocator",
			want:   "internal/webui/api_tasks.go:42: undefined: forgeLocator",
		},
		{
			name:   "a non-search tool keeps its first informative line",
			tool:   "find",
			output: "/home/example/projects/app\n/home/example/projects/app/internal",
			want:   "/home/example/projects/app",
		},
		{
			name:   "module-cache noise is skipped",
			tool:   "read",
			output: "/home/example/go/pkg/mod/github.com/x/y@v1.0/z.go\npackage y",
			want:   "package y",
		},
		{
			name:   "empty output is reported as completed without a preview",
			tool:   "shell",
			output: "   \n  ",
			want:   "completed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderToolCall(ToolCallEvent{Name: tc.tool, Output: tc.output})
			if !strings.Contains(got, tc.want) {
				t.Errorf("preview missing %q:\n%s", tc.want, got)
			}
			// The renderer must never state a count it computed from its own
			// truncated sample: that produced two different numbers for one
			// result set ("194 more lines" beside "… 3 more lines").
			if strings.Contains(got, "more lines") && !strings.Contains(got, "lines total") {
				t.Errorf("renderer invented a line count from its own sample:\n%s", got)
			}
		})
	}
}

// TestToolRenderIsBoundedAndAggregatable: one rendered call must stay small so
// a tool-heavy turn cannot crowd the answer out of a single Telegram frame.
func TestToolRenderIsBoundedAndAggregatable(t *testing.T) {
	output := strings.Repeat("/very/long/path/to/some/file.go:1234:    someLineOfSource();\n", 50)
	got := RenderToolCall(ToolCallEvent{Name: "grep", Output: output})
	if n := len([]rune(got)); n > 220 {
		t.Errorf("one rendered tool call is %d runes, want <= 220:\n%s", n, got)
	}
	// 14 such calls must leave the majority of the frame for the answer.
	if n := len([]rune(got)) * 14; n > 3900/2 {
		t.Errorf("14 rendered calls would consume %d of a 3900-rune frame", n)
	}
}
