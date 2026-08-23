package gateway

import (
	"encoding/json"
	"strings"
)

// toolSummaryMaxRunes bounds a rendered tool summary. Tool output is
// unbounded  --  a grep can return thousands of lines  --  and the summary is
// one line in a chat transcript, so it is cut rather than allowed to bury the
// answer it precedes.
const (
	toolSummaryMaxRunes    = 80
	toolParametersMaxRunes = 120
	redactedParameterValue = "[redacted]"
)

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

// RenderToolCall returns the channel-neutral compact representation used by
// chat surfaces. Parameters are intentionally omitted: JSON/schema-shaped
// inputs are noisy, often contain secrets, and are not useful progress text.
// The bounded fenced block keeps multiline command output readable without
// allowing one result to bury the answer or exceed Telegram's message limit.
func (e ToolCallEvent) RenderToolCall() string {
	header := "🔧 " + strings.TrimSpace(e.Name)
	if line := firstNonEmptyLine(e.Err); line != "" {
		return header + " — ❌ " + truncateRunes(line, toolSummaryMaxRunes)
	}
	if strings.TrimSpace(e.Output) == "" {
		return header + " — done"
	}
	const maxOutputRunes = 480
	output := strings.TrimSpace(e.Output)
	output = truncateRunes(output, maxOutputRunes)
	// A nested fence would terminate the block early in Markdown renderers.
	output = strings.ReplaceAll(output, "```", "''' ")
	return header + "\n```text\n" + output + "\n```"
}

// SummarizeToolParameters returns a bounded JSON shape of tool input. Every
// string value is replaced rather than echoed because credentials can appear
// inside ordinary parameters such as command, content, or body. Values under
// secret-bearing keys use an explicit redaction marker; other strings retain
// only their type. Invalid JSON is never echoed.
func SummarizeToolParameters(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "[unavailable]"
	}
	value = redactToolParameters(value, false)
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[unavailable]"
	}
	return truncateRunesWithinLimit(string(encoded), toolParametersMaxRunes)
}

func redactToolParameters(value any, sensitive bool) any {
	if sensitive {
		return redactedParameterValue
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = redactToolParameters(child, sensitiveParameterKey(key))
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = redactToolParameters(child, sensitive)
		}
		return typed
	case string:
		return "[string]"
	default:
		return value
	}
}

func sensitiveParameterKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(key))
	for _, marker := range []string{
		"token", "secret", "password", "passwd", "api_key", "apikey",
		"authorization", "credential", "cookie", "private_key",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func truncateRunesWithinLimit(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit <= 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

// MediaEvent reports one media attachment produced during a turn, so a
// channel can deliver it (e.g. through gateway.MediaSender) in the same
// ordered pass as the text and tool activity that surrounded it.
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
	// turn. A channel that cannot deliver it inline (see
	// gateway.CapabilitiesOf) is expected to fall back to rendering the
	// attachment's URL as text instead of dropping it silently.
	Media(event MediaEvent)
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

// Media discards the event: a text-only caller has nowhere to put it.
func (f DeltaFunc) Media(MediaEvent) {}

// toolCallRecorder wraps a TurnStream, forwarding every event to it
// unchanged while also recording the tool calls in arrival order. The
// recording happens regardless of whether next is nil (a non-streaming
// caller, e.g. Router.LLM) so a turn's tool activity is captured for
// persistence and later replay even when nothing rendered it live the first
// time.
type toolCallRecorder struct {
	next   TurnStream
	events []ToolCallEvent
}

// Delta forwards the fragment to the wrapped stream, if any.
func (r *toolCallRecorder) Delta(text string) {
	if r.next != nil {
		r.next.Delta(text)
	}
}

// ToolCall records the event, then forwards it to the wrapped stream, if
// any.
func (r *toolCallRecorder) ToolCall(event ToolCallEvent) {
	r.events = append(r.events, event)
	if r.next != nil {
		r.next.ToolCall(event)
	}
}

// Media forwards the event to the wrapped stream, if any. Unlike ToolCall,
// it is not recorded: media persistence and replay are not yet a supported
// path (see archie-core-1786748942243-6-f109697e), so recording it here
// would create a ledger field nothing reads.
func (r *toolCallRecorder) Media(event MediaEvent) {
	if r.next != nil {
		r.next.Media(event)
	}
}

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
