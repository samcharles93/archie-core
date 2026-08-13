package gateway

import "strings"

// toolSummaryMaxRunes bounds a rendered tool summary. Tool output is
// unbounded  --  a grep can return thousands of lines  --  and the summary is
// one line in a chat transcript, so it is cut rather than allowed to bury the
// answer it precedes.
const toolSummaryMaxRunes = 80

// ToolCallEvent reports one completed tool invocation within a turn. It
// carries the raw outcome rather than rendered text: presentation (emoji,
// layout, whether to show it at all) belongs to the channel adapter.
type ToolCallEvent struct {
	// Name is the tool the model invoked.
	Name string
	// Output is what the tool returned, verbatim.
	Output string
	// Err is non-empty when the tool failed, in which case Output is
	// unreliable and usually empty.
	Err string
}

// Summary reduces the outcome to a single short line suitable for an inline
// status entry. It never returns an empty string: a tool that returned
// nothing still ran, and a blank summary would read as a rendering fault.
func (e ToolCallEvent) Summary() string {
	if line := firstNonEmptyLine(e.Err); line != "" {
		return "failed: " + truncateRunes(line, toolSummaryMaxRunes)
	}
	if line := firstNonEmptyLine(e.Output); line != "" {
		return truncateRunes(line, toolSummaryMaxRunes)
	}
	return "done"
}

// TurnStream receives a turn's output as it is produced.
//
// It replaces the plain delta callback so a channel can render tool activity
// in the same ordered pass as the text: both arrive on the generating
// goroutine, in the order the model produced them. Implementations must not
// block  --  a slow renderer stalls generation  --  and must be safe to call
// from that goroutine only.
//
// Callers that render text alone use DeltaFunc rather than implementing this.
type TurnStream interface {
	// Delta appends the next fragment of assistant text.
	Delta(text string)
	// ToolCall reports a tool invocation that has finished executing.
	ToolCall(event ToolCallEvent)
}

// DeltaFunc adapts a plain text-delta callback to TurnStream for callers that
// render text only. Tool events are discarded. A nil DeltaFunc is inert, so
// DeltaFunc(nil) is a usable "stream nothing" sink.
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

// firstNonEmptyLine returns the first line of s that holds something other
// than whitespace, trimmed. It returns "" when there is no such line.
func firstNonEmptyLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// truncateRunes shortens s to at most maxRunes runes, marking the cut with an
// ellipsis. Counting runes rather than bytes keeps multi-byte text from being
// cut mid-character.
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}
