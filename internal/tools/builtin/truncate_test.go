package builtin_test

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools/builtin"
)

func TestTruncateHead_NoTruncation(t *testing.T) {
	content := "line1\nline2\nline3"
	result := builtin.TruncateHead(content, 100, 100000)

	if result.Truncated {
		t.Fatal("should not be truncated")
	}
	if result.Content != content {
		t.Fatalf("content mismatch: got %q", result.Content)
	}
}

func TestTruncateHead_ByLines(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "x"
	}
	content := strings.Join(lines, "\n")

	result := builtin.TruncateHead(content, 10, 100000)

	if !result.Truncated {
		t.Fatal("should be truncated")
	}
	if result.OriginalLine != 100 {
		t.Fatalf("expected 100 original lines, got %d", result.OriginalLine)
	}
	if !strings.Contains(result.Content, "[truncated:") {
		t.Fatal("should contain truncation notice")
	}
	if !strings.Contains(result.Content, "10/100 lines") {
		t.Fatalf("truncation notice should mention line counts, got: %s", result.Content)
	}
}

func TestTruncateHead_ByBytes(t *testing.T) {
	content := strings.Repeat("a", 1000)

	result := builtin.TruncateHead(content, 10000, 100)

	if !result.Truncated {
		t.Fatal("should be truncated by bytes")
	}
	if result.OriginalSize != 1000 {
		t.Fatalf("expected 1000 original bytes, got %d", result.OriginalSize)
	}
}

func TestTruncateTail_NoTruncation(t *testing.T) {
	content := "line1\nline2\nline3"
	result := builtin.TruncateTail(content, 100, 100000)

	if result.Truncated {
		t.Fatal("should not be truncated")
	}
	if result.Content != content {
		t.Fatalf("content mismatch")
	}
}

func TestTruncateTail_ByLines(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "x"
	}
	content := strings.Join(lines, "\n")

	result := builtin.TruncateTail(content, 10, 100000)

	if !result.Truncated {
		t.Fatal("should be truncated")
	}
	if !strings.Contains(result.Content, "last 10/100 lines") {
		t.Fatalf("truncation notice should mention line counts, got: %s", result.Content)
	}
	// The kept content should be the last 10 lines (all "x").
	kept := strings.Split(strings.TrimSpace(result.Content), "\n")
	// Last 10 lines + truncation notice line + blank line = 12.
	if len(kept) < 10 {
		t.Fatalf("expected at least 10 kept lines, got %d", len(kept))
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		input    int
		expected string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{51200, "50.0KB"},
		{1048576, "1.0MB"},
	}
	for _, tc := range cases {
		got := builtin.FormatSize(tc.input)
		if got != tc.expected {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
