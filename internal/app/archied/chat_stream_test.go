package archied

import (
	"strings"
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
	s.events = append(s.events, "tool:"+event.Name+":"+event.Parameters+":"+event.Summary())
}

func (s *recordingStream) Media(event gateway.MediaEvent) {
	// Path is recorded alongside the type: a local attachment that lost
	// its path on the way through would still look like a media event.
	s.events = append(s.events, "media:"+event.ToolName+":"+event.Attachment.Type+":"+event.Attachment.Path)
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
			wantEvents: []string{"text:checking", `tool:shell:{"cmd":"[string]"}:exit 0`, "text: done"},
		},
		{
			name: "a failed tool is reported as a failure, not silence",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{ToolName: "shell", Error: "exit status 2"}},
			},
			wantEvents: []string{"tool:shell::failed: exit status 2"},
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
		{
			// A tool's output carrying a tools.MultimodalResult envelope
			// must report both the ordinary tool-call event (so the turn
			// narrates what ran) and a Media event per URL, so the
			// channel has something to actually deliver.
			name: "a tool result carrying MultimodalResult URLs reports Media",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{ToolCallID: "1", ToolName: "generate_video"}},
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{
					ToolCallID: "1",
					ToolName:   "generate_video",
					Output:     `{"is_multimodal":true,"urls":[{"type":"video","url":"https://x/v"}]}`,
				}},
			},
			wantEvents: []string{
				`tool:generate_video::{"is_multimodal":true,"urls":[{"type":"video","url":"https://x/v"}]}`,
				"media:generate_video:video:",
			},
		},
		{
			// A local file has no URL and must not acquire one: the
			// channel uploads it. Losing Path here is exactly the
			// silent non-delivery send_file exists to end.
			name: "a tool result carrying a local path reports Media with that path",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{ToolCallID: "1", ToolName: "send_file"}},
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{
					ToolCallID: "1",
					ToolName:   "send_file",
					Output:     `{"is_multimodal":true,"urls":[{"type":"document","path":"/tmp/a.md"}]}`,
				}},
			},
			wantEvents: []string{
				"tool:send_file::" + `{"is_multimodal":true,"urls":[{"type":"document","path":"/tmp/a.md"}]}`,
				"media:send_file:document:/tmp/a.md",
			},
		},
		{
			// A ref naming neither a URL nor a path is not deliverable,
			// and an empty attachment would render as a delivered file.
			name: "a media ref with no source reports no Media event",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{ToolCallID: "1", ToolName: "odd_tool"}},
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{
					ToolCallID: "1",
					ToolName:   "odd_tool",
					Output:     `{"is_multimodal":true,"urls":[{"type":"document"}]}`,
				}},
			},
			wantEvents: []string{"tool:odd_tool::" + `{"is_multimodal":true,"urls":[{"type":"document"}]}`},
		},
		{
			// An ordinary JSON tool result that happens to be a JSON
			// object but isn't a MultimodalResult (no is_multimodal, or
			// false) must not be misread as carrying media.
			name: "a plain JSON tool result reports no Media event",
			parts: []core.StreamPart{
				{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{ToolCallID: "1", ToolName: "read_file"}},
				{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{
					ToolCallID: "1",
					ToolName:   "read_file",
					Output:     `{"path":"a.txt","content":"hello"}`,
				}},
			},
			wantEvents: []string{`tool:read_file::{"path":"a.txt","content":"hello"}`},
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

func TestDrainChatStreamCorrelatesBoundsAndRedactsToolParameters(t *testing.T) {
	secret := strings.Repeat("s", 200)
	sink := &recordingStream{}
	drainChatStream(streamOf(
		core.StreamPart{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{
			ToolCallID: "call-2",
			ToolName:   "shell",
			Input:      `{"cmd":"curl -H 'Authorization: Bearer ` + secret + `'","nested":{"api_token":"` + secret + `"}}`,
		}},
		core.StreamPart{Type: core.StreamPartToolCall, ToolCall: &core.ToolCall{
			ToolCallID: "call-1",
			ToolName:   "read",
			Input:      `{"path":"/tmp/one"}`,
		}},
		core.StreamPart{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{
			ToolCallID: "call-1",
			ToolName:   "read",
			Output:     "one",
		}},
		core.StreamPart{Type: core.StreamPartToolResult, ToolResult: &core.ToolResult{
			ToolCallID: "call-2",
			ToolName:   "shell",
			Output:     "ok",
		}},
	), sink)

	if len(sink.events) != 2 {
		t.Fatalf("events = %v, want two completed calls", sink.events)
	}
	if sink.events[0] != `tool:read:{"path":"[string]"}:one` {
		t.Fatalf("first event = %q, want parameters correlated by call ID", sink.events[0])
	}
	if strings.Contains(sink.events[1], secret) {
		t.Fatalf("second event exposed secret parameters: %q", sink.events[1])
	}
	if !strings.Contains(sink.events[1], `"api_token":"[redacted]"`) {
		t.Fatalf("second event = %q, want redacted token", sink.events[1])
	}
	if len([]rune(sink.events[1])) > 180 {
		t.Fatalf("second event is %d runes, want bounded parameters", len([]rune(sink.events[1])))
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
