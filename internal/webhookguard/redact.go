package webhookguard

import (
	"encoding/json"
	"fmt"
	"strings"
)

// redactedValue replaces a value whose key looks sensitive.
const redactedValue = "[redacted]"

// sensitiveKeyMarkers is the key-name heuristic, matching
// internal/gateway/stream.go's sensitiveParameterKey exactly so the two
// redaction paths agree on what counts as sensitive.
var sensitiveKeyMarkers = []string{
	"token", "secret", "password", "passwd", "api_key", "apikey",
	"authorization", "credential", "cookie", "private_key",
}

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
// make that impossible. Only a value whose key matches the marker list is
// touched; everything else is returned exactly as decoded.
//
// A key match redacts its entire value, including a nested object or array
// -- "credentials": {"host": ..., "user": ...} is wholesale replaced, not
// recursed into, matching the intent of the key name.
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
	m, ok := value.(map[string]any)
	if !ok {
		if arr, ok := value.([]any); ok {
			for i, child := range arr {
				arr[i] = redactValue(child)
			}
		}
		return value
	}
	for key, child := range m {
		if sensitiveKey(key) {
			m[key] = redactedValue
			continue
		}
		m[key] = redactValue(child)
	}
	return m
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
