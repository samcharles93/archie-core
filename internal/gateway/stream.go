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
	// A notice the tool printed is the only trustworthy source for how much
	// came back; anything counted here describes the visible sample.
	if summary, ok := toolNoticeSummary(content); ok {
		return summary
	}
	// Only a search gets a counted summary. A compiler or linter diagnostic has
	// the same "file.go:42: message" shape as a search hit, so counting every
	// tool's output would report a build error as "3 matches" and hide the very
	// output the operator asked for.
	if isSearchTool(name) {
		if summary, ok := searchResultSummary(content); ok {
			return summary
		}
	}
	preview := firstInformativeLine(content)
	if preview == "" {
		return "completed with no printable preview"
	}
	return preview
}

// searchToolNames are the tools whose output is a set of matches, and so reads
// better counted than sampled. Every other tool is described by its first
// informative line.
var searchToolNames = map[string]bool{
	"grep":       true,
	"rg":         true,
	"search":     true,
	"toolsearch": true,
}

func isSearchTool(name string) bool {
	return searchToolNames[strings.ToLower(strings.TrimSpace(name))]
}

// toolNoticeSummary renders the "how much came back" summary from the
// truncation notice the tool printed, if any.
//
// Two notice shapes exist (internal/tools/builtin): shell prints "[truncated:
// showing last N/M lines, …]" and the other tools print "[truncated: showing
// N/M lines, …]". Both are metadata, never content, so both must be recognised
// -- missing the "last" form would quote the raw notice as the preview, which
// is the exact defect this change removes.
//
// The same notice also tells us WHICH END was kept, which changes the summary:
// a head-truncation ("showing 3/197") lost the tail, so "showing 3" describes
// the sample; a tail-truncation ("showing last 66/97") lost the head, so the
// count of what is visible says nothing useful about the whole.
func toolNoticeSummary(content string) (string, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		notice, ok := parseTruncationNotice(strings.TrimSpace(line))
		if !ok {
			continue
		}
		if notice.total <= notice.shown {
			continue // showing everything is not a truncation
		}
		if notice.truncatedHead {
			return fmt.Sprintf("%d lines total; showing last %d", notice.total, notice.shown), true
		}
		return fmt.Sprintf("%d lines total; showing %d", notice.total, notice.shown), true
	}
	return "", false
}

// truncationNotice describes how much a tool reported and which end it kept.
type truncationNotice struct {
	shown         int
	total         int
	truncatedHead bool // the kept lines are the tail ("showing last N/M")
}

// parseTruncationNotice reads "<shown>/<total> lines" out of a truncation
// notice, accepting both the plain and the "showing last" form.
//
// The "[truncated: … ]" wrapper is required, not tolerated. It is the only
// marker that distinguishes a notice the tool printed from ordinary output that
// happens to contain the same phrasing: both built-in producers emit the
// wrapper (internal/tools/builtin/truncate.go), so a bare "showing 3/197 lines"
// inside a log or a document is content and must be left alone rather than
// replaced by a summary.
func parseTruncationNotice(line string) (truncationNotice, bool) {
	_, rest, found := strings.Cut(line, "[truncated: ")
	if !found {
		return truncationNotice{}, false
	}
	_, rest, found = strings.Cut(rest, "showing ")
	if !found {
		return truncationNotice{}, false
	}
	truncatedHead := false
	if after, ok := strings.CutPrefix(rest, "last "); ok {
		rest, truncatedHead = after, true
	}
	counts, _, found := strings.Cut(rest, " lines")
	if !found {
		return truncationNotice{}, false
	}
	left, right, found := strings.Cut(counts, "/")
	if !found {
		return truncationNotice{}, false
	}
	shown, errShown := strconv.Atoi(strings.TrimSpace(left))
	total, errTotal := strconv.Atoi(strings.TrimSpace(right))
	if errShown != nil || errTotal != nil {
		return truncationNotice{}, false
	}
	return truncationNotice{shown: shown, total: total, truncatedHead: truncatedHead}, true
}

// searchResultSummary counts the match lines in ripgrep-shaped output. Only
// matches are counted: with context requested, ripgrep also emits context lines
// ("path-12-text", hyphen separator) and "--" group separators, and counting
// those would report the match count plus every context line around it.
func searchResultSummary(content string) (string, bool) {
	matches := countSearchMatches(content)
	if matches == 0 {
		return "", false
	}
	return fmt.Sprintf("%d %s", matches, pluralize(matches, "match", "matches")), true
}

// countSearchMatches counts lines using ripgrep's match separator (":").
func countSearchMatches(content string) int {
	matches := 0
	for line := range strings.SplitSeq(content, "\n") {
		if isSearchMatchLine(strings.TrimSpace(line)) {
			matches++
		}
	}
	return matches
}

// isSearchMatchLine reports whether line has ripgrep's "path:N:text" match
// shape -- a colon separator. The hyphen form ("path-N-text") is a CONTEXT
// line, not a match, and "--" is a group separator between context blocks;
// neither is a match and neither is counted.
//
// The path may itself contain colons, so the separator is located by scanning
// from the end for a colon followed by digits and another colon.
func isSearchMatchLine(line string) bool {
	if line == "" || strings.HasPrefix(line, "[") {
		return false
	}
	if strings.Trim(line, "-") == "" {
		return false // "--" group separator
	}
	for i := len(line) - 1; i > 0; i-- {
		if line[i] != ':' {
			continue
		}
		digits := line[i+1:]
		end := 0
		for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
			end++
		}
		// Require <path>:<digits>:<text> -- the trailing colon distinguishes a
		// match from a "path:42" reference with nothing after it.
		if end > 0 && end < len(digits) && digits[end] == ':' {
			return true
		}
	}
	return false
}

// firstInformativeLine returns the first line worth showing: not blank, not a
// module-cache path, and not a truncation notice (which is metadata the notice
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
		if _, ok := parseTruncationNotice(line); ok {
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
