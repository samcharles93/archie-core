package webhookguard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// redactedValue replaces a value whose key (or shape) looks sensitive.
const redactedValue = "[redacted]"

// SensitiveKeyMarkers are key substrings whose values are redacted.
var SensitiveKeyMarkers = []string{
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

// RedactPayload replaces values under sensitive keys, or with secret-shaped
// values, with a marker and returns the re-encoded JSON. Best-effort.
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
	for _, marker := range SensitiveKeyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
