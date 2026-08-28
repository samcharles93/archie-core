// Lifted from tau internal/agent/tools/truncate.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultMaxBytes is the maximum byte size for tool output sent to the LLM.
	DefaultMaxBytes = 50 * 1024 // 50KB

	// DefaultMaxLines is the maximum line count for tool output sent to the LLM.
	DefaultMaxLines = 2000

	// DefaultToolTimeout is the per-tool execution deadline. Tools that exceed
	// this are cancelled. The shell tool uses its own configurable timeout.
	DefaultToolTimeout = 60 * time.Second
)

// TruncationResult holds the potentially truncated content and metadata.
type TruncationResult struct {
	Content      string
	Truncated    bool
	OriginalSize int
	OriginalLine int
	OutputLines  int // number of complete lines kept in Content
}

// TruncateHeadRaw keeps the first N lines/bytes, dropping the tail, without
// appending a truncation notice. Callers append their own context-specific
// notice (e.g. read's "Use offset=N to continue"). Never returns partial lines:
// if the first line alone exceeds maxBytes, Content is empty with OutputLines 0.
func TruncateHeadRaw(content string, maxLines, maxBytes int) TruncationResult {
	originalSize := len(content)
	originalLines := 0
	if content != "" {
		originalLines = strings.Count(content, "\n") + 1
	}

	result := TruncationResult{
		Content:      content,
		OriginalSize: originalSize,
		OriginalLine: originalLines,
		OutputLines:  originalLines,
	}

	if originalSize <= maxBytes && originalLines <= maxLines {
		return result
	}

	lines := strings.SplitAfter(content, "\n")
	var b strings.Builder
	lineCount := 0

	for _, line := range lines {
		if lineCount >= maxLines || b.Len()+len(line) > maxBytes {
			result.Truncated = true
			break
		}
		b.WriteString(line)
		lineCount++
	}

	result.Content = strings.TrimSuffix(b.String(), "\n")
	result.OutputLines = lineCount
	return result
}

// TruncateHead keeps the first N lines/bytes, dropping the tail, and appends
// a generic truncation notice. Good for search results and listings.
func TruncateHead(content string, maxLines, maxBytes int) TruncationResult {
	result := TruncateHeadRaw(content, maxLines, maxBytes)
	if result.Truncated {
		result.Content += fmt.Sprintf(
			"\n\n[truncated: showing %d/%d lines, %s/%s]",
			result.OutputLines, result.OriginalLine,
			FormatSize(len(result.Content)), FormatSize(result.OriginalSize),
		)
	}
	return result
}

// TruncateTail keeps the last N lines/bytes, dropping the head.
// Good for logs, command output.
func TruncateTail(content string, maxLines, maxBytes int) TruncationResult {
	originalSize := len(content)
	originalLines := 0
	if content != "" {
		originalLines = strings.Count(content, "\n") + 1
	}

	result := TruncationResult{
		Content:      content,
		OriginalSize: originalSize,
		OriginalLine: originalLines,
	}

	if originalSize <= maxBytes && originalLines <= maxLines {
		return result
	}

	lines := strings.Split(content, "\n")

	// Work backwards to find start index.
	startIdx := len(lines)
	byteCount := 0
	lineCount := 0

	for i, line := range slices.Backward(lines) {
		lineLen := len(line)
		if i < len(lines)-1 {
			lineLen++ // account for the \n
		}
		if lineCount >= maxLines || byteCount+lineLen > maxBytes {
			break
		}
		byteCount += lineLen
		lineCount++
		startIdx = i
	}

	if startIdx > 0 {
		result.Truncated = true
		kept := strings.Join(lines[startIdx:], "\n")
		result.Content = fmt.Sprintf(
			"[truncated: showing last %d/%d lines, %s/%s]\n\n%s",
			lineCount, originalLines,
			FormatSize(len(kept)), FormatSize(originalSize),
			kept,
		)
	}

	return result
}

// FormatSize returns a human-readable size string.
func FormatSize(bytes int) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
