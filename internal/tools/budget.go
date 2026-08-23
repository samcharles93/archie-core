package tools

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// writeSpillFile writes content to a fresh private file in dir, returning an
// empty path when writing fails so callers can fall back to inline truncation.
func writeSpillFile(dir, toolName string, content []byte) string {
	f, err := os.CreateTemp(dir, "spill-"+sanitiseToolName(toolName)+"-*")
	if err != nil {
		return ""
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return ""
	}
	if _, err := f.Write(content); err != nil {
		return ""
	}
	return f.Name()
}

func sanitiseToolName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

// CapPayload bounds one tool result before it reaches the model. Oversized
// results are spilled when spillDir is configured and writable; otherwise they
// are explicitly truncated on a rune boundary. There is deliberately no
// aggregate per-turn accounting or stopping condition.
func CapPayload(toolName, payload string, limit int, spillDir string) string {
	if limit <= 0 || len(payload) <= limit {
		return payload
	}
	if spillDir != "" {
		if path := writeSpillFile(spillDir, toolName, []byte(payload)); path != "" {
			return fmt.Sprintf(
				"[%s: result spilled to %s (%d characters); too large to inline]",
				toolName, path, len(payload),
			)
		}
	}
	return truncateAtRuneBoundary(payload, limit) + fmt.Sprintf(
		"\n\n[%s: result truncated, showing the first %d of %d characters]",
		toolName, limit, len(payload),
	)
}

func truncateAtRuneBoundary(s string, limit int) string {
	if limit >= len(s) {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
