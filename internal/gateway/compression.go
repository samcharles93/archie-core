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

	// MaxPromptTokens is the effective history budget after reserving space
	// for the system prompt, tools, and model output. A positive value takes
	// precedence over ContextWindow*Threshold.
	MaxPromptTokens int

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

// CompressionConfigForModel derives the history budget from the selected
// model rather than assuming every provider has the default context window.
// promptReserve is the estimated system/tool prompt cost already known before
// history is assembled.
func CompressionConfigForModel(details ModelDetails, promptReserve int) (CompressionConfig, error) {
	cfg := DefaultCompressionConfig()
	if details.ContextWindow <= 0 {
		// ModelDetails is an optional extension on ModelManager. Preserve
		// compatibility for minimal adapters, while production catalog-backed
		// managers still take the model-specific path below.
		cfg.MaxPromptTokens = max(cfg.ContextWindow-max(promptReserve, 0), 1)
		return cfg, nil
	}

	reserve := max(promptReserve, 0) + max(details.MaxOutputTokens, 0)
	if reserve >= details.ContextWindow {
		return CompressionConfig{}, fmt.Errorf("model %q context window %d is smaller than reserved prompt/output budget %d", details.Ref, details.ContextWindow, reserve)
	}
	cfg.ContextWindow = details.ContextWindow
	cfg.MaxPromptTokens = max(details.ContextWindow-reserve, 1)
	return cfg, nil
}

// tokenEstimate returns a rough estimate of the token count for a string.
// ASCII text keeps the common four-characters-per-token approximation. Every
// non-ASCII rune reserves one token because byte/rune averaging substantially
// undercounts CJK text and emoji, which providers commonly tokenize more
// densely than English text.
func tokenEstimate(s string) int {
	ascii, nonASCII := 0, 0
	for _, r := range s {
		if r < 128 {
			ascii++
			continue
		}
		nonASCII++
	}
	return ascii/4 + nonASCII
}

// EstimateTokens exposes the same conservative estimate used by compression
// so callers can reserve space for system prompts and tool descriptions.
func EstimateTokens(s string) int {
	return tokenEstimate(s)
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
	threshold := cfg.MaxPromptTokens
	if threshold <= 0 {
		threshold = int(float64(cfg.ContextWindow) * cfg.Threshold)
	}

	if totalTokens <= threshold || len(messages) == 0 {
		return CompressedView{
			Messages:     messages,
			TokensBefore: totalTokens,
			TokensAfter:  totalTokens,
		}
	}

	// Compression: keep first N + summary marker + last N. If the protected
	// tail itself exceeds the effective budget, reduce it until the resulting
	// view fits. This matters for small-context local models.
	protectLast := min(cfg.ProtectLast, len(messages))
	protectFirst := min(cfg.ProtectFirst, len(messages)-protectLast)
	for {
		truncated := messages[protectFirst : len(messages)-protectLast]
		summary := summariseTruncated(truncated, cfg)

		compressed := make([]CompressedMessage, 0, protectFirst+1+protectLast)
		compressed = append(compressed, messages[:protectFirst]...)
		compressed = append(compressed, CompressedMessage{Role: "system", Content: summary})
		compressed = append(compressed, messages[len(messages)-protectLast:]...)
		compressedTokens := estimateTotal(compressed)
		if compressedTokens <= threshold || (protectFirst == 0 && protectLast == 0) {
			if compressedTokens > threshold && protectFirst == 0 && protectLast == 0 {
				maxSummaryChars := max(threshold*4, 1)
				compressed[0].Content = truncate(compressed[0].Content, maxSummaryChars)
				compressedTokens = estimateTotal(compressed)
			}
			return CompressedView{
				Messages:       compressed,
				WasCompressed:  true,
				TokensBefore:   totalTokens,
				TokensAfter:    compressedTokens,
				ProtectedFirst: protectFirst,
				ProtectedLast:  protectLast,
			}
		}
		if protectLast > 0 {
			protectLast--
			continue
		}
		protectFirst--
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
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
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
