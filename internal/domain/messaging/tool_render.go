package messaging

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/webhookguard"
)

const (
	toolParametersMaxRunes = 120
	redactedParameterValue = "[redacted]"
)

// RenderToolCall returns the channel-neutral compact representation used by
// chat surfaces. Parameters are intentionally omitted: JSON/schema-shaped
// inputs are noisy, often contain secrets, and are not useful progress text.
func RenderToolCall(e ToolCallEvent) string {
	if limit, ok := legacyTurnBudgetLimit(e.Err); ok {
		return toolProgressBlock("tools", "stopped", fmt.Sprintf("tool-output limit reached (%s chars); further results suppressed", limit))
	}
	if line := cleanToolError(e.Name, e.Err); line != "" {
		return toolProgressBlock(e.Name, "failed", line)
	}
	return toolProgressBlock(e.Name, "done", toolPreview(e.Name, e.Output))
}

// FailureKey identifies equivalent tool calls for channel adapters that
// collapse repeated activity.
func FailureKey(e ToolCallEvent) string {
	if _, ok := legacyTurnBudgetLimit(e.Err); ok {
		return "legacy-turn-output-limit"
	}
	if line := cleanToolError(e.Name, e.Err); line != "" {
		return strings.TrimSpace(e.Name) + "\x00" + line
	}
	return "success\x00" + strings.TrimSpace(e.Name) + "\x00" + toolPreview(e.Name, e.Output)
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

func toolPreview(name, output string) string {
	content, _ := unwrapToolContent(output)
	if strings.TrimSpace(content) == "" {
		return "completed"
	}
	if summary, ok := toolNoticeSummary(content); ok {
		return summary
	}
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

var searchToolNames = map[string]bool{
	"grep":       true,
	"rg":         true,
	"search":     true,
	"toolsearch": true,
}

func isSearchTool(name string) bool {
	return searchToolNames[strings.ToLower(strings.TrimSpace(name))]
}

func toolNoticeSummary(content string) (string, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		notice, ok := parseTruncationNotice(strings.TrimSpace(line))
		if !ok {
			continue
		}
		if notice.total <= notice.shown {
			continue
		}
		if notice.truncatedHead {
			return fmt.Sprintf("%d lines total; showing last %d", notice.total, notice.shown), true
		}
		return fmt.Sprintf("%d lines total; showing %d", notice.total, notice.shown), true
	}
	return "", false
}

type truncationNotice struct {
	shown         int
	total         int
	truncatedHead bool
}

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

func searchResultSummary(content string) (string, bool) {
	matches := countSearchMatches(content)
	if matches == 0 {
		return "", false
	}
	return fmt.Sprintf("%d %s", matches, pluralize(matches, "match", "matches")), true
}

func countSearchMatches(content string) int {
	matches := 0
	for line := range strings.SplitSeq(content, "\n") {
		if isSearchMatchLine(strings.TrimSpace(line)) {
			matches++
		}
	}
	return matches
}

func isSearchMatchLine(line string) bool {
	if line == "" || strings.HasPrefix(line, "[") {
		return false
	}
	if strings.Trim(line, "-") == "" {
		return false
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
		if end > 0 && end < len(digits) && digits[end] == ':' {
			return true
		}
	}
	return false
}

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

// SummarizeToolParameters returns a bounded JSON shape of tool input.
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
