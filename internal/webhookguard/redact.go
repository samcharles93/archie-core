package webhookguard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// redactedValue replaces a value whose key (or shape) looks sensitive.
const redactedValue = "[redacted]"

// sensitiveKeyMarkers is the key-name heuristic, matching
// internal/gateway/stream.go's sensitiveParameterKey exactly so the two
// redaction paths agree on what counts as sensitive. It is intentionally
// conservative: a value whose key matches is redacted wholesale, so a marker
// that is too broad (e.g. bare "key" or "auth", which would match "Key: 42"
// or "author") would destroy the schema-by-example mapping use case the
// captured payload exists to serve.
var sensitiveKeyMarkers = []string{
	"token", "secret", "password", "passwd", "pwd", "passphrase",
	"api_key", "apikey", "api_secret", "client_secret", "private_key", "secret_key",
	"signing_key", "authorization", "bearer", "credential", "cookie", "session",
	"access_token", "refresh_token", "id_token", "bot_token", "webhook_secret",
	"x_api_key", "x_auth_token", "jwt", "oauth", "csrf", "otp", "signature",
}

// jwtTokenRe matches a compact JWT (header.payload.signature) -- the header's
// base64url always begins "eyJ" -- which is an unambiguous secret token even
// when a sender puts it under a key the name heuristic misses.
var jwtTokenRe = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// RedactPayload replaces values under sensitive-looking JSON keys with a
// redaction marker and returns the re-encoded payload, for capturing an
// inbound webhook body before persisting it.
//
// This intentionally does NOT reuse internal/gateway/stream.go's
// SummarizeToolParameters shape unchanged: that function replaces every
// string value regardless of key, which produces a bounded one-line summary
// for a chat transcript. A captured webhook payload exists to be inspected
// and mapped field-by-field (see docs/prds/event-sources-and-reactions.md
// and t2db.3's schema-by-example mapping) -- redacting every string would
// make that impossible. Only a value whose key matches the marker list, or
// whose value is an unambiguous secret shape, is touched; everything else is
// returned exactly as decoded.
//
// A key match redacts its entire value, including a nested object or array
// -- "credentials": {"host": ..., "user": ...} is wholesale replaced, not
// recursed into, matching the intent of the key name.
//
// This is best-effort, not a guarantee: a sender that names a secret field
// something the heuristic misses is still captured with its secret.
func RedactPayload(payload []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("redact payload: decode: %w", err)
	}
	redacted := redactValue(value)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("redact payload: encode: %w", err)
	}
	return encoded, nil
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveKey(key) {
				typed[key] = redactedValue
				continue
			}
			typed[key] = redactValue(child)
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = redactValue(child)
		}
		return typed
	case string:
		if sensitiveValueShape(typed) {
			return redactedValue
		}
		return typed
	default:
		return value
	}
}

// sensitiveValueShape reports whether a string is an unambiguous secret even
// under a non-sensitive key: a compact JWT or a PEM private-key block. These
// shapes are never a meaningful field-mapping value, so redacting them does
// not hurt the schema-by-example use case.
func sensitiveValueShape(s string) bool {
	if len(s) < 20 {
		return false
	}
	if jwtTokenRe.MatchString(s) {
		return true
	}
	return strings.Contains(s, "-----BEGIN") && strings.Contains(s, "PRIVATE KEY-----")
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(key))
	for _, marker := range sensitiveKeyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
