package gateway

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/webhookguard"
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

// RenderToolCall returns the channel-neutral compact representation used by
// chat surfaces. Parameters are intentionally omitted: JSON/schema-shaped
// inputs are noisy, often contain secrets, and are not useful progress text.
// Results are reduced semantically instead of clipped blindly: file contents,
// listings and structured envelopes are described, never echoed.
// RenderToolCall returns the channel-neutral compact representation used by
// chat surfaces. (A function rather than a method because ToolCallEvent is a
// messaging alias: see chat_contract_aliases.go.)
func RenderToolCall(e ToolCallEvent) string {
	if limit, ok := legacyTurnBudgetLimit(e.Err); ok {
		return toolProgressBlock("tools", "stopped", fmt.Sprintf("tool-output limit reached (%s chars); further results suppressed", limit))
	}
	if line := cleanToolError(e.Name, e.Err); line != "" {
		return toolProgressBlock(e.Name, "failed", line)
	}
	return toolProgressBlock(e.Name, "done", toolPreview(e.Name, e.Output))
}

// FailureKey identifies equivalent failures for channel adapters that collapse
// retries. Legacy aggregate-output-limit errors deliberately share one key
// across tool names: they are one obsolete turn-level condition, not five
// independently useful failures.
// FailureKey identifies equivalent failures for channel adapters that
// collapse retries. (A function rather than a method because ToolCallEvent
// is a messaging alias: see chat_contract_aliases.go.)
func FailureKey(e ToolCallEvent) string {
	if _, ok := legacyTurnBudgetLimit(e.Err); ok {
		return "legacy-turn-output-limit"
	}
	if line := cleanToolError(e.Name, e.Err); line != "" {
		return strings.TrimSpace(e.Name) + "\x00" + line
	}
	return ""
}

func toolProgressBlock(name, status, preview string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	preview = strings.ReplaceAll(strings.TrimSpace(preview), "```", "''' ")
	if preview == "" {
		preview = "completed"
	}
	return "🔧 " + name + " — " + status + "\n```text\n" + truncateRunes(preview, 180) + "\n```"
}

// toolPreview reduces a tool result to one informative line.
//
// The result is described, never quoted: a search returns match lines that say
// nothing on their own (a path, a line number, a closing brace), so the preview
// states what came back and how much of it. The count comes from the tool's own
// truncation notice when it printed one, because a count recomputed from the
// visible sample describes the sample, not the result set -- rendering both
// produced two different numbers for one search ("194 more lines" next to
// "… 3 more lines"), which reads as noise.
func toolPreview(name, output string) string {
	content, _ := unwrapToolContent(output)
	if strings.TrimSpace(content) == "" {
		return "completed"
	}
	// A tool that reported a real total is the only trustworthy source for
	// how much came back; anything we compute is about the sample we can see.
	if total, ok := toolReportedTotal(content); ok {
		return total
	}
	if summary, ok := searchResultSummary(content); ok {
		return summary
	}
	preview := firstInformativeLine(content)
	if preview == "" {
		return "completed with no printable preview"
	}
	_ = name
	return preview
}

// toolReportedTotal extracts the line total from the truncation notices the
// tool layer prints (internal/tools/builtin: "[truncated: showing 2/194 lines,
// …]"). Those notices are metadata, not content, so they are never rendered as
// a preview line -- quoting one is what produced "[truncated: showing 3/197
// lines, 412B/24.1KB]" as if it were a search hit.
//
// A notice that shows everything (showing N/N) is not a truncation and gets no
// summary: the content speaks for itself.
func toolReportedTotal(content string) (string, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		shown, total, ok := parseTruncationNotice(line)
		if !ok {
			continue
		}
		if total <= shown {
			continue
		}
		return fmt.Sprintf("%d lines total; showing %d", total, shown), true
	}
	return "", false
}

// parseTruncationNotice reads "showing <shown>/<total> lines" out of a
// truncation notice, tolerating the surrounding "[truncated: … ]" wrapper.
func parseTruncationNotice(line string) (shown, total int, ok bool) {
	_, rest, found := strings.Cut(line, "showing ")
	if !found {
		return 0, 0, false
	}
	counts, _, found := strings.Cut(rest, " lines")
	if !found {
		return 0, 0, false
	}
	left, right, found := strings.Cut(counts, "/")
	if !found {
		return 0, 0, false
	}
	shown, errShown := strconv.Atoi(strings.TrimSpace(left))
	total, errTotal := strconv.Atoi(strings.TrimSpace(right))
	if errShown != nil || errTotal != nil {
		return 0, 0, false
	}
	return shown, total, true
}

// searchResultSummary counts match lines in ripgrep-shaped output ("path-12-
// text", "path:12:text", or a context separator). A search is the one tool
// whose raw output is least useful quoted and most useful counted, so it is
// summarised rather than sampled.
func searchResultSummary(content string) (string, bool) {
	matches, _ := countSearchMatches(content)
	if matches == 0 {
		return "", false
	}
	return fmt.Sprintf("%d %s", matches, pluralize(matches, "match", "matches")), true
}

// countSearchMatches counts match lines and whether any were truncated.
func countSearchMatches(content string) (matches int, truncated bool) {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if searchMatchLine(line) {
			matches++
		}
	}
	return matches, false
}

// searchMatchLine reports whether line has ripgrep's "path-N-text" or
// "path:N:text" shape. The path may contain colons, so the separator is found
// by scanning for the last one that is followed by a line number.
func searchMatchLine(line string) bool {
	if strings.HasPrefix(line, "--") && strings.Trim(line, "-") == "" {
		return true // context separator
	}
	for i := len(line) - 1; i > 0; i-- {
		if line[i] != ':' && line[i] != '-' {
			continue
		}
		digits := line[i+1:]
		end := 0
		for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
			end++
		}
		if end == 0 || end >= len(digits) {
			continue
		}
		if digits[end] == ':' || digits[end] == '-' {
			return true
		}
	}
	return false
}

// firstInformativeLine returns the first line worth showing: not blank, not a
// module-cache path, and not a truncation notice (which is metadata the total
// path already consumed).
func firstInformativeLine(content string) string {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "/pkg/mod/") {
			continue
		}
		if _, _, ok := parseTruncationNotice(line); ok {
			continue
		}
		return truncateRunes(line, 120)
	}
	return ""
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func unwrapToolContent(output string) (string, bool) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", false
	}
	var envelope struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(trimmed), &envelope) == nil && envelope.Content != "" {
		return envelope.Content, true
	}
	var quoted string
	if json.Unmarshal([]byte(trimmed), &quoted) == nil {
		return quoted, true
	}
	return output, false
}

func cleanToolError(name, raw string) string {
	line := firstNonEmptyLine(raw)
	prefix := "tool " + strings.TrimSpace(name) + ": "
	line = strings.TrimPrefix(line, prefix)
	return truncateRunes(strings.TrimSpace(line), toolSummaryMaxRunes)
}

func legacyTurnBudgetLimit(raw string) (string, bool) {
	const marker = "turn budget exceeded ("
	_, after, ok := strings.Cut(raw, marker)
	if !ok {
		return "", false
	}
	rest := after
	end := strings.Index(rest, " chars)")
	if end < 1 {
		return "", false
	}
	return rest[:end], true
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
	for _, marker := range webhookguard.SensitiveKeyMarkers {
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
