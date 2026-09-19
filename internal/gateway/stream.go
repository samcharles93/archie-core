package gateway

import "github.com/samcharles93/archie-core/internal/domain/messaging"

// RenderToolCall returns the channel-neutral compact representation used by chat surfaces.
func RenderToolCall(e ToolCallEvent) string {
	return messaging.RenderToolCall(e)
}

// FailureKey identifies equivalent tool calls for channel adapters that collapse repeated activity.
func FailureKey(e ToolCallEvent) string {
	return messaging.FailureKey(e)
}

// SummarizeToolParameters returns a bounded JSON shape of tool input.
func SummarizeToolParameters(input string) string {
	return messaging.SummarizeToolParameters(input)
}

// DeltaFunc adapts a plain text-delta callback to TurnStream.
type DeltaFunc func(string)

// Delta forwards the fragment to the wrapped callback.
func (f DeltaFunc) Delta(text string) {
	if f == nil {
		return
	}
	f(text)
}

// ToolCall discards the event: a text-only caller has nowhere to put it.
func (f DeltaFunc) ToolCall(ToolCallEvent) {}

// Media discards the event: a text-only caller has nowhere to put it.
func (f DeltaFunc) Media(MediaEvent) {}

type toolCallRecorder struct {
	next   TurnStream
	events []ToolCallEvent
}

func (r *toolCallRecorder) Delta(text string) {
	if r.next != nil {
		r.next.Delta(text)
	}
}

func (r *toolCallRecorder) ToolCall(event ToolCallEvent) {
	r.events = append(r.events, event)
	if r.next != nil {
		r.next.ToolCall(event)
	}
}

func (r *toolCallRecorder) Media(event MediaEvent) {
	if r.next != nil {
		r.next.Media(event)
	}
}
