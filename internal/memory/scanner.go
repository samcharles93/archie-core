package memory

import "regexp"

// ThreatLevel indicates the severity of a threat detected by a Scanner.
type ThreatLevel int

const (
	// ThreatNone means no suspicious patterns were found.
	ThreatNone ThreatLevel = iota

	// ThreatWarn means a potentially problematic pattern was found, but the
	// write should be allowed with a warning logged.
	ThreatWarn

	// ThreatBlock means a dangerous pattern was found and the write should
	// be rejected.
	ThreatBlock
)

// ThreatResult describes a single threat detected during content scanning.
type ThreatResult struct {
	// Level indicates the severity: none, warn, or block.
	Level ThreatLevel

	// Pattern is the regexp that matched (for audit logging).
	Pattern string

	// Message is a human-readable description of the threat.
	Message string
}

// Scanner inspects memory content for dangerous or sensitive patterns before
// the content is persisted. Implementations are pluggable; the zero-value
// DefaultScanner is a reasonable default for production use.
type Scanner interface {
	// ScanContent examines content and returns a ThreatResult. A result
	// with Level == ThreatNone means the content is clean.
	ScanContent(content string) ThreatResult
}

// DefaultScanner is the built-in Scanner implementation. It checks for
// prompt-injection patterns (block-level) and sensitive-data leakage
// (warn-level). The zero value is usable.
type DefaultScanner struct{}

// Compile-time check: DefaultScanner implements Scanner.
var _ Scanner = (*DefaultScanner)(nil)

// ── Patterns ────────────────────────────────────────────────────────────
//
// These patterns are compiled once at init time. They are intentionally
// broad to catch common variants without being so broad that they
// produce false positives on normal memory content.

var (
	promptInjectionPatterns = []*regexp.Regexp{
		// Override / ignore previous instructions
		regexp.MustCompile(`(?i)(ignore|forget|override|disregard)\s+(all\s+)?(the\s+)?(previous|above|prior|earlier)\s+(instructions?|directions?|commands?|prompts?)`),

		// Role-change / identity attacks
		regexp.MustCompile(`(?i)(you\s+(are|now)\s+|act\s+as\s+|pretend\s+(to\s+be|you\s+are)\s+|roleplay\s+as\s+)(now\s+)?(an?\s+)?(different|new|another|unrestricted|uncensored)`),

		// Delimiter injection (common in adversarial prompts)
		regexp.MustCompile(`(?i)<\|im_start\|>`),
		regexp.MustCompile(`(?i)<\|im_end\|>`),

		// System prompt extraction attempts
		regexp.MustCompile(`(?i)(print|output|repeat|display|show|tell\s+me)\s+(your\s+)?(system\s+)?(prompt|instructions?|directions?)\s*(exactly|verbatim)?`),
	}

	sensitiveDataPatterns = []*regexp.Regexp{
		// API keys and secrets with common key=value patterns
		regexp.MustCompile(`(?i)(api[_\s-]?key|apikey|api[_\s-]?secret)\s*[:=]\s*['"\x60]?[\w-]{8,}['"\x60]?`),

		// Passwords in key=value form
		regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*['"\x60]?\S{4,}['"\x60]?`),

		// Generic secrets and tokens
		regexp.MustCompile(`(?i)(secret|token|credential|authorization)\s*[:=]\s*['"\x60]?[\w-]{8,}['"\x60]?`),

		// Private key blocks (PEM format)
		regexp.MustCompile(`(?i)-----BEGIN\s+(RSA\s+|EC\s+|DSA\s+|OPENSSH\s+)?PRIVATE\s+KEY-----`),

		// JWT tokens (base64-encoded header.payload.signature pattern)
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	}
)

// ScanContent examines content against the prompt-injection and
// sensitive-data patterns. Prompt-injection matches produce ThreatBlock;
// sensitive-data matches produce ThreatWarn. The first match wins.
func (s *DefaultScanner) ScanContent(content string) ThreatResult {
	for _, p := range promptInjectionPatterns {
		if m := p.FindString(content); m != "" {
			return ThreatResult{
				Level:   ThreatBlock,
				Pattern: p.String(),
				Message: "content matches prompt injection pattern: " + truncateMatch(m),
			}
		}
	}
	for _, p := range sensitiveDataPatterns {
		if m := p.FindString(content); m != "" {
			return ThreatResult{
				Level:   ThreatWarn,
				Pattern: p.String(),
				Message: "content may contain sensitive data: " + truncateMatch(m),
			}
		}
	}
	return ThreatResult{Level: ThreatNone}
}

// truncateMatch limits the matched string for log/error messages so that
// a long match (e.g. an entire private key) is not echoed in full.
func truncateMatch(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
