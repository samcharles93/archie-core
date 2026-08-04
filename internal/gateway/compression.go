package gateway

import (
	"fmt"
	"sort"
	"strings"
)

// CompressionConfig controls automatic context compression for long-running
// chat sessions. Compression keeps the conversation history within the model's
// context window by summarising older messages when the total estimated token
// count exceeds a threshold.
type CompressionConfig struct {
	// Enabled toggles automatic compression. When false, compression is
	// skipped regardless of other settings.
	Enabled bool

	// Threshold is the fraction of the context window at which compression
	// triggers. For example, 0.5 means compress when estimated tokens
	// exceed 50% of ContextWindow. Must be between 0 and 1.
	Threshold float64

	// ContextWindow is the model's maximum context size in tokens (not
	// characters). Used with Threshold to decide when to compress.
	ContextWindow int

	// ProtectFirst keeps the first N messages unmodified (typically the
	// system greeting and initial instructions).
	ProtectFirst int

	// ProtectLast keeps the last N messages unmodified (the most recent
	// conversation turns).
	ProtectLast int

	// SummaryMarker is inserted between the truncated middle and the
	// protected tail to indicate compression occurred.
	SummaryMarker string
}

// DefaultCompressionConfig returns a CompressionConfig with sensible
// defaults: compression at 50% threshold, protecting
// first 3 and last 20 messages. ContextWindow defaults to 128k.
func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		Enabled:       true,
		Threshold:     0.5,
		ContextWindow: 128000,
		ProtectFirst:  3,
		ProtectLast:   20,
		SummaryMarker: "[Earlier conversation has been summarised to stay within context limits.]",
	}
}

// tokenEstimate returns a rough estimate of the token count for a string.
// Uses character / 4 which is a common approximation for English text.
func tokenEstimate(s string) int {
	return len(s) / 4
}

// CompressedView holds the compressed representation of a conversation
// history.
type CompressedView struct {
	// Messages is the compressed message list ready for the LLM.
	Messages []CompressedMessage

	// WasCompressed is true when compression was applied.
	WasCompressed bool

	// TokensBefore is the estimated token count before compression.
	TokensBefore int

	// TokensAfter is the estimated token count after compression.
	TokensAfter int

	// ProtectedFirst and ProtectedLast are how many leading and trailing
	// input messages passed through untouched, and are only meaningful when
	// WasCompressed is true.
	//
	// They exist so a caller persisting the result can write back the
	// original records for those positions rather than rebuilding them from
	// Role and Content. A rebuilt message is a different message: it loses
	// its canonical MessageID, its upstream SourceID and its timestamp, which
	// breaks redelivery deduplication permanently and contradicts the
	// immutability requirement in docs/architecture/messaging-and-work-intake
	// .md lines 112-114.
	ProtectedFirst int
	ProtectedLast  int
}

// CompressedMessage is a message in a compressed conversation view.
type CompressedMessage struct {
	Role    string
	Content string
}

// CompressHistory applies context compression to a list of messages. When
// the estimated token count is below the threshold, messages are returned
// unchanged. When compression triggers, messages between ProtectFirst
// and ProtectLast are replaced with a summary marker.
//
// The caller supplies messages as role/content pairs (e.g. from
// session store history) and gets back a compressed view.
func CompressHistory(messages []CompressedMessage, cfg CompressionConfig) CompressedView {
	if !cfg.Enabled || cfg.ContextWindow <= 0 {
		return CompressedView{
			Messages:    messages,
			TokensAfter: estimateTotal(messages),
		}
	}

	totalTokens := estimateTotal(messages)
	threshold := int(float64(cfg.ContextWindow) * cfg.Threshold)

	if totalTokens <= threshold || len(messages) <= cfg.ProtectFirst+cfg.ProtectLast {
		return CompressedView{
			Messages:     messages,
			TokensBefore: totalTokens,
			TokensAfter:  totalTokens,
		}
	}

	// Compression: keep first N + summary marker + last N.
	protectLast := min(cfg.ProtectLast, len(messages))
	protectFirst := min(cfg.ProtectFirst, len(messages)-protectLast)

	// Build a summary of the truncated middle.
	truncated := messages[protectFirst : len(messages)-protectLast]
	summary := summariseTruncated(truncated, cfg)

	compressed := make([]CompressedMessage, 0, protectFirst+1+protectLast)
	compressed = append(compressed, messages[:protectFirst]...)
	compressed = append(compressed, CompressedMessage{Role: "system", Content: summary})
	compressed = append(compressed, messages[len(messages)-protectLast:]...)

	compressedTokens := estimateTotal(compressed)

	return CompressedView{
		Messages:       compressed,
		WasCompressed:  true,
		TokensBefore:   totalTokens,
		TokensAfter:    compressedTokens,
		ProtectedFirst: protectFirst,
		ProtectedLast:  protectLast,
	}
}

// SummaryIndex returns the position of the generated summary within Messages,
// or -1 when nothing was compressed. The summary is the one element of a
// compressed view that is genuinely new; everything else corresponds to an
// input message at a known offset.
func (v CompressedView) SummaryIndex() int {
	if !v.WasCompressed {
		return -1
	}
	return v.ProtectedFirst
}

// estimateTotal returns the sum of estimated token counts across messages.
func estimateTotal(messages []CompressedMessage) int {
	total := 0
	for _, m := range messages {
		total += tokenEstimate(m.Content)
	}
	return total
}

// summariseTruncated produces a summary of messages removed during
// compression. Without an LLM summarisation call (deferred to the
// memory provider), we produce a statistical summary that tells the
// model what was removed.
func summariseTruncated(messages []CompressedMessage, cfg CompressionConfig) string {
	if len(messages) == 0 {
		return cfg.SummaryMarker
	}

	// Count turns by role.
	var userTurns, assistantTurns int
	for _, m := range messages {
		switch {
		case strings.EqualFold(m.Role, "user"):
			userTurns++
		case strings.EqualFold(m.Role, "assistant"):
			assistantTurns++
		}
	}

	// Find the topics from the first and last message.
	first := truncate(messages[0].Content, 80)
	last := truncate(messages[len(messages)-1].Content, 80)

	// Estimate tokens removed.
	removedTokens := estimateTotal(messages)

	return fmt.Sprintf(
		"%s\n\n[%d messages removed (~%d tokens): %d user turns, %d assistant turns. "+
			"From: %q ... To: %q]",
		cfg.SummaryMarker,
		len(messages),
		removedTokens,
		userTurns,
		assistantTurns,
		first,
		last,
	)
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Simple byte truncation is fine for ASCII; for multi-byte we'd
	// need rune-safe slicing. English text is the primary case.
	for i := n; i < len(s); i++ {
		if s[i] < 128 {
			return s[:i] + "..."
		}
	}
	return s[:n] + "..."
}

// SortBySeq implements sort.Interface for ordering compressed messages
// by their original sequence position. Used when reconstructing message
// order after decompression (future feature).
type SortBySeq []struct {
	Seq  int
	Role string
	Text string
}

func (s SortBySeq) Len() int           { return len(s) }
func (s SortBySeq) Less(i, j int) bool { return s[i].Seq < s[j].Seq }
func (s SortBySeq) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// Ensure sort.Interface is satisfied.
var _ sort.Interface = SortBySeq(nil)
