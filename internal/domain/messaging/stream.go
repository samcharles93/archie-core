package messaging

import "strings"

// ToolCallEvent reports one completed tool invocation within a turn. It
// carries the raw outcome rather than rendered text: presentation (emoji,
// layout, whether to show it at all) belongs to the channel adapter.
type ToolCallEvent struct {
	// ID correlates the completed result with the model's invocation.
	ID string
	// Name is the tool the model invoked.
	Name string
	// Parameters is a bounded, redacted JSON summary of the invocation input.
	// It is safe for channel adapters to render directly.
	Parameters string
	// Output is what the tool returned, verbatim.
	Output string
	// Err is non-empty when the tool failed, in which case Output is
	// unreliable and usually empty.
	Err string
}

// MediaEvent reports one media attachment produced during a turn, so a
// channel can deliver it (e.g. through its media sender capability) in the
// same ordered pass as the text and tool activity that surrounded it.
type MediaEvent struct {
	// ToolName is the tool call that produced the attachment.
	ToolName string
	// Attachment is the media to deliver.
	Attachment MediaAttachment
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
	// Media reports a media attachment a tool call produced during the
	// turn. A channel that cannot deliver it inline is expected to fall
	// back to rendering the attachment's URL as text instead of dropping
	// it silently.
	Media(event MediaEvent)
}

// toolSummaryMaxRunes bounds a rendered tool summary.
const toolSummaryMaxRunes = 80

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

// firstNonEmptyLine returns the first non-empty line of s, trimmed.
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
