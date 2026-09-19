package gateway

import (
	"testing"
)

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
	stream.Media(MediaEvent{ToolName: "video_gen"})
}

// A text-only caller has nowhere to put media, so DeltaFunc must discard it
// exactly like it discards ToolCall  --  never panic, never route it into the
// text stream as a raw URL the caller didn't ask for.
func TestDeltaFuncDiscardsMedia(t *testing.T) {
	var got []string
	stream := DeltaFunc(func(delta string) { got = append(got, delta) })

	stream.Media(MediaEvent{ToolName: "video_gen", Attachment: MediaAttachment{Type: "video", URL: "https://example.com/v.mp4"}})

	if len(got) != 0 {
		t.Fatalf("Media leaked into the text stream: %v", got)
	}
}

// toolCallRecorder must forward Media to the wrapped stream unchanged, the
// same contract it already honors for Delta and ToolCall.
func TestToolCallRecorderForwardsMedia(t *testing.T) {
	next := &recordingStream{}
	r := &toolCallRecorder{next: next}

	event := MediaEvent{ToolName: "video_gen", Attachment: MediaAttachment{Type: "video", URL: "https://example.com/v.mp4"}}
	r.Media(event)

	want := []string{"media:video_gen:video"}
	if len(next.events) != len(want) || next.events[0] != want[0] {
		t.Fatalf("events = %v, want %v", next.events, want)
	}
}

// toolCallRecorder must not panic when wrapping a nil stream (the
// non-streaming caller case Delta/ToolCall already handle).
func TestToolCallRecorderMediaNilNextIsInert(t *testing.T) {
	r := &toolCallRecorder{}
	r.Media(MediaEvent{ToolName: "video_gen"})
}
