package main

import (
	"testing"

	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// recordingStream captures the turn's reported progress in arrival order.
type recordingStream struct {
	events []string
}

func (s *recordingStream) Delta(text string) {
	s.events = append(s.events, "text:"+text)
}

func (s *recordingStream) ToolCall(event gateway.ToolCallEvent) {
	s.events = append(s.events, "tool:"+event.Name+":"+event.Summary())
}

func streamOf(parts ...core.StreamPart) <-chan core.StreamPart {
	ch := make(chan core.StreamPart, len(parts))
	for _, part := range parts {
		ch <- part
	}
	close(ch)
	return ch
}

func TestDrainChatStream(t *testing.T) {
	tests := []struct {
		name       string
		parts      []core.StreamPart
		wantText   string
		wantEvents []string
	}{
		{
			name: "text only",
			parts: []core.StreamPart{
				{Type: core.StreamPartTextDelta, TextDelta: "hello "},
				{Type: core.StreamPartTextDelta, TextDelta: "world"},
			},
			wantText:   "hello world",
			wantEvents: []string{"text:hello ", "text:world"},
		},
		{
			name: "a tool result is reported between the text around it",
			parts: []core.StreamPart{
				{Type: core.StreamPartTextDelta, TextDelta: "checking"},
				{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{ToolCallID: "1", ToolName: "shell", Input: `{"cmd":"true"}`}},
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{ToolCallID: "1", ToolName: "shell", Output: "exit 0"}},
				{Type: core.StreamPartTextDelta, TextDelta: " done"},
			},
			wantText:   "checking done",
			wantEvents: []string{"text:checking", "tool:shell:exit 0", "text: done"},
		},
		{
			name: "a failed tool is reported as a failure, not silence",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{ToolName: "shell", Error: "exit status 2"}},
			},
			wantEvents: []string{"tool:shell:failed: exit status 2"},
		},
		{
			// The tool call part carries no outcome yet: reporting it as
			// well would show every tool twice.
			name: "the tool call part alone reports nothing",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{ToolName: "shell"}},
			},
			wantEvents: nil,
		},
		{
			name: "parts with nothing to render are skipped",
			parts: []core.StreamPart{
				{Type: core.StreamPartStartStep},
				{Type: core.StreamPartTextDelta, TextDelta: ""},
				{Type: core.StreamPartReasoningDelta, ReasoningDelta: "thinking"},
				{Type: core.StreamPartToolResult},
				{Type: core.StreamPartFinishStep},
				{Type: core.StreamPartFinish},
			},
			wantEvents: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &recordingStream{}
			text := drainChatStream(streamOf(tc.parts...), sink)

			if text != tc.wantText {
				t.Fatalf("text = %q, want %q", text, tc.wantText)
			}
			if len(sink.events) != len(tc.wantEvents) {
				t.Fatalf("events = %v, want %v", sink.events, tc.wantEvents)
			}
			for i := range tc.wantEvents {
				if sink.events[i] != tc.wantEvents[i] {
					t.Fatalf("events = %v, want %v", sink.events, tc.wantEvents)
				}
			}
		})
	}
}

// The stream must be drained to close even when nobody is rendering it:
// ai-sdk writes to FullStream synchronously, so an abandoned stream stalls
// the generating goroutine.
func TestDrainChatStreamWithoutASinkStillCollectsText(t *testing.T) {
	text := drainChatStream(streamOf(
		core.StreamPart{Type: core.StreamPartTextDelta, TextDelta: "a"},
		core.StreamPart{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{ToolName: "shell", Output: "ok"}},
		core.StreamPart{Type: core.StreamPartTextDelta, TextDelta: "b"},
	), nil)

	if text != "ab" {
		t.Fatalf("text = %q, want %q", text, "ab")
	}
}
