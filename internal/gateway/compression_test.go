package gateway

import (
	"strings"
	"testing"
)

func TestCompressionConfigForModelReservesPromptAndOutputBudget(t *testing.T) {
	details := ModelDetails{ContextWindow: 16384, MaxOutputTokens: 2048}
	cfg, err := CompressionConfigForModel(details, 1024)
	if err != nil {
		t.Fatalf("CompressionConfigForModel() error = %v", err)
	}

	if got, want := cfg.MaxPromptTokens, 13312; got != want {
		t.Fatalf("MaxPromptTokens = %d, want %d", got, want)
	}
	if got, want := cfg.ContextWindow, 16384; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
}

func TestCompressionConfigForModelUsesCompatibilityFallbackWithoutMetadata(t *testing.T) {
	cfg, err := CompressionConfigForModel(ModelDetails{Ref: "provider/model"}, 1024)
	if err != nil {
		t.Fatalf("CompressionConfigForModel() error = %v", err)
	}
	if cfg.MaxPromptTokens != 126976 {
		t.Fatalf("MaxPromptTokens = %d, want compatibility budget", cfg.MaxPromptTokens)
	}
}

func TestCompressionConfigForModelRejectsOverReservedBudget(t *testing.T) {
	_, err := CompressionConfigForModel(ModelDetails{Ref: "provider/model", ContextWindow: 4096, MaxOutputTokens: 2048}, 2048)
	if err == nil {
		t.Fatal("CompressionConfigForModel() error = nil, want over-reserved-budget error")
	}
}

func TestCompressHistoryHonorsPromptBudget(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.MaxPromptTokens = 1000
	cfg.ProtectFirst = 1
	cfg.ProtectLast = 2
	msgs := makeMessages(40, 300)

	view := CompressHistory(msgs, cfg)
	if !view.WasCompressed {
		t.Fatal("expected compression")
	}
	if view.TokensAfter > cfg.MaxPromptTokens {
		t.Fatalf("TokensAfter = %d, want <= %d", view.TokensAfter, cfg.MaxPromptTokens)
	}
}

func TestCompressHistoryThresholdNotExceeded(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.ContextWindow = 1000000 // huge window

	msgs := makeMessages(100, 500) // ~12,500 estimated tokens
	view := CompressHistory(msgs, cfg)

	if view.WasCompressed {
		t.Error("should not compress below threshold")
	}
	if len(view.Messages) != len(msgs) {
		t.Errorf("expected %d messages, got %d", len(msgs), len(view.Messages))
	}
}

func TestCompressHistoryTruncatesMiddle(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.ContextWindow = 10000 // small window forces compression
	cfg.Threshold = 0.1
	cfg.ProtectFirst = 3
	cfg.ProtectLast = 5

	msgs := makeMessages(50, 200) // ~2,500 estimated tokens
	view := CompressHistory(msgs, cfg)

	if !view.WasCompressed {
		t.Fatal("should have compressed")
	}
	if view.TokensAfter >= view.TokensBefore {
		t.Errorf("tokens after (%d) should be less than before (%d)",
			view.TokensAfter, view.TokensBefore)
	}

	// Structure: first 3 + system summary + last 5 = 9 messages.
	if len(view.Messages) != 9 {
		t.Errorf("expected 9 messages (3 + summary + 5), got %d", len(view.Messages))
	}

	// First 3 should be unchanged.
	for i := range 3 {
		if view.Messages[i].Content != msgs[i].Content {
			t.Errorf("first message %d changed", i)
		}
	}

	// Summary marker should be a system message.
	summary := view.Messages[3]
	if summary.Role != "system" {
		t.Errorf("summary role = %q, want system", summary.Role)
	}
	if !strings.Contains(summary.Content, "messages removed") {
		t.Errorf("summary should mention messages removed: %q", summary.Content)
	}

	// Last 5 should be the tail.
	for i := range 5 {
		got := view.Messages[4+i].Content
		want := msgs[len(msgs)-5+i].Content
		if got != want {
			t.Errorf("tail message %d: got %q, want %q", i, got, want)
		}
	}
}

func TestCompressHistorySmallConversationStillHonorsPromptBudget(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.MaxPromptTokens = 100
	cfg.ProtectFirst = 3
	cfg.ProtectLast = 20
	msgs := makeMessages(4, 200)

	view := CompressHistory(msgs, cfg)
	if !view.WasCompressed {
		t.Fatal("expected compression")
	}
	if view.TokensAfter > cfg.MaxPromptTokens {
		t.Fatalf("TokensAfter = %d, want <= %d", view.TokensAfter, cfg.MaxPromptTokens)
	}
}

func TestCompressHistorySmallConversation(t *testing.T) {
	cfg := DefaultCompressionConfig()

	// Only 15 messages — protect 3+20 requires at least 23, so
	// this small conversation should not compress.
	msgs := makeMessages(15, 100)
	view := CompressHistory(msgs, cfg)

	if view.WasCompressed {
		t.Error("small conversation should not compress")
	}
}

func TestCompressHistoryDisabled(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.Enabled = false
	cfg.ContextWindow = 100 // tiny window

	msgs := makeMessages(100, 200)
	view := CompressHistory(msgs, cfg)

	if view.WasCompressed {
		t.Error("disabled compression should not trigger")
	}
}

func TestCompressHistoryZeroWindow(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.ContextWindow = 0 // disabled

	msgs := makeMessages(100, 200)
	view := CompressHistory(msgs, cfg)

	if view.WasCompressed {
		t.Error("zero context window should skip compression")
	}
}

func TestTokenEstimate(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"four", 1},        // 4 chars / 4 = 1
		{"hello world", 2}, // 11 chars / 4 = 2
		{"12345678", 2},    // 8 chars / 4 = 2
		{"你好世界", 4},        // non-ASCII runes reserve one token each
		{"🙂🙂", 2},          // emoji must not be byte-divided to zero
		{strings.Repeat("x", 100), 25},
	}

	for _, tt := range tests {
		got := tokenEstimate(tt.input)
		if got != tt.want {
			t.Errorf("tokenEstimate(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEstimateTotal(t *testing.T) {
	msgs := []CompressedMessage{
		{Role: "user", Content: "hello"},       // 5 → 1
		{Role: "assistant", Content: "world!"}, // 6 → 1
	}
	got := estimateTotal(msgs)
	if got != 2 {
		t.Errorf("estimateTotal = %d, want 2", got)
	}
}

func TestSummariseTruncated(t *testing.T) {
	cfg := DefaultCompressionConfig()
	msgs := []CompressedMessage{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "reply one"},
		{Role: "user", Content: "second message"},
		{Role: "assistant", Content: "reply two"},
	}

	summary := summariseTruncated(msgs, cfg)
	if !strings.Contains(summary, "4 messages removed") {
		t.Errorf("summary should mention count: %q", summary)
	}
	if !strings.Contains(summary, "2 user turns") {
		t.Errorf("summary should mention user turns: %q", summary)
	}
	if !strings.Contains(summary, "2 assistant turns") {
		t.Errorf("summary should mention assistant turns: %q", summary)
	}
}

func TestSummariseTruncatedEmpty(t *testing.T) {
	cfg := DefaultCompressionConfig()
	summary := summariseTruncated(nil, cfg)
	if summary != cfg.SummaryMarker {
		t.Errorf("empty summary = %q, want marker", summary)
	}
}

func TestDefaultCompressionConfig(t *testing.T) {
	cfg := DefaultCompressionConfig()
	if !cfg.Enabled {
		t.Error("default should be enabled")
	}
	if cfg.Threshold != 0.5 {
		t.Errorf("Threshold = %v, want 0.5", cfg.Threshold)
	}
	if cfg.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000", cfg.ContextWindow)
	}
	if cfg.ProtectFirst != 3 {
		t.Errorf("ProtectFirst = %d, want 3", cfg.ProtectFirst)
	}
	if cfg.ProtectLast != 20 {
		t.Errorf("ProtectLast = %d, want 20", cfg.ProtectLast)
	}
}

func TestCompressHistoryProtectClamp(t *testing.T) {
	cfg := DefaultCompressionConfig()
	cfg.ContextWindow = 1000
	cfg.Threshold = 0.01 // compress with tiny window
	cfg.ProtectFirst = 10
	cfg.ProtectLast = 10

	// Only 15 messages total: protect 10+10=20 would overflow.
	msgs := makeMessages(15, 200)
	view := CompressHistory(msgs, cfg)

	if view.WasCompressed {
		// If it did compress, first and last should be properly clamped.
		if len(view.Messages) >= len(msgs) {
			t.Errorf("compressed %d messages should be fewer than %d",
				len(view.Messages), len(msgs))
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short string: %q", got)
	}
	if got := truncate("hello world this is long", 10); len(got) > 10+3 {
		t.Errorf("long string too long: %q (%d)", got, len(got))
	}
}

// makeMessages creates n alternating user/assistant messages, each
// contentLen characters long.
func makeMessages(n, contentLen int) []CompressedMessage {
	out := make([]CompressedMessage, n)
	for i := range n {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out[i] = CompressedMessage{
			Role:    role,
			Content: strings.Repeat("x", contentLen) + " msg " + itoa(i),
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
