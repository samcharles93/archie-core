package gateway

import (
	"strings"
	"testing"
)

func TestToolCallEventSummary(t *testing.T) {
	tests := []struct {
		name  string
		event ToolCallEvent
		want  string
	}{
		{
			name:  "single line output is the summary",
			event: ToolCallEvent{Name: "memory_edit", Output: "added entry"},
			want:  "added entry",
		},
		{
			name:  "only the first non-empty line is kept",
			event: ToolCallEvent{Name: "read", Output: "\n\nfirst line\nsecond line"},
			want:  "first line",
		},
		{
			name:  "surrounding whitespace is trimmed",
			event: ToolCallEvent{Name: "shell", Output: "  exit 0  \n"},
			want:  "exit 0",
		},
		{
			name:  "an error replaces the output",
			event: ToolCallEvent{Name: "shell", Output: "partial", Err: "exit status 2"},
			want:  "failed: exit status 2",
		},
		{
			name:  "a multi-line error is reduced the same way",
			event: ToolCallEvent{Name: "shell", Err: "exit status 2\nstack trace"},
			want:  "failed: exit status 2",
		},
		{
			name:  "no output at all still reads as a completed call",
			event: ToolCallEvent{Name: "write"},
			want:  "done",
		},
		{
			name:  "whitespace-only output reads as a completed call",
			event: ToolCallEvent{Name: "write", Output: "   \n  "},
			want:  "done",
		},
		{
			name:  "a long line is truncated with an ellipsis",
			event: ToolCallEvent{Name: "grep", Output: strings.Repeat("x", toolSummaryMaxRunes+10)},
			want:  strings.Repeat("x", toolSummaryMaxRunes) + "…",
		},
		{
			name:  "truncation counts runes, not bytes",
			event: ToolCallEvent{Name: "grep", Output: strings.Repeat("é", toolSummaryMaxRunes+10)},
			want:  strings.Repeat("é", toolSummaryMaxRunes) + "…",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.event.Summary(); got != tc.want {
				t.Fatalf("Summary() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A caller that only renders text must be able to keep passing a plain
// callback, and must never be handed a tool event it cannot use.
func TestDeltaFuncIsTextOnly(t *testing.T) {
	var got []string
	stream := DeltaFunc(func(delta string) { got = append(got, delta) })

	stream.Delta("one")
	stream.ToolCall(ToolCallEvent{Name: "shell", Output: "exit 0"})
	stream.Delta("two")

	want := []string{"one", "two"}
	if len(got) != len(want) {
		t.Fatalf("deltas = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("deltas = %v, want %v", got, want)
		}
	}
}

// RouteStream and TurnRunner accept a nil sink to mean "not streaming", so a
// nil DeltaFunc must be inert rather than a panic waiting for the first token.
func TestDeltaFuncNilIsInert(t *testing.T) {
	stream := DeltaFunc(nil)
	stream.Delta("anything")
	stream.ToolCall(ToolCallEvent{Name: "shell"})
}
