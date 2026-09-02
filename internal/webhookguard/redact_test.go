package webhookguard_test

import (
	"encoding/json"
	"testing"

	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// TestRedactPayloadPreservesNonSensitiveValues is the regression test for
// the defect a naive port of gateway/stream.go's redaction would introduce:
// that function replaces every string value regardless of key, which is
// correct for a bounded chat-transcript summary but would make a captured
// webhook payload useless for schema-by-example mapping (t2db.3) -- an
// operator cannot map a field they cannot see. Only values under a
// sensitive-looking key may be redacted; everything else must survive
// byte-for-byte.
func TestRedactPayloadPreservesNonSensitiveValues(t *testing.T) {
	input := []byte(`{"issue":{"number":42,"title":"Fix the thing","open":true}}`)

	got, err := webhookguard.RedactPayload(input)
	if err != nil {
		t.Fatalf("RedactPayload() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}
	issue, ok := decoded["issue"].(map[string]any)
	if !ok {
		t.Fatalf("decoded[%q] = %#v, want map[string]any", "issue", decoded["issue"])
	}
	if issue["title"] != "Fix the thing" {
		t.Errorf("title = %v, want unredacted %q", issue["title"], "Fix the thing")
	}
	if issue["number"] != float64(42) {
		t.Errorf("number = %v, want unredacted 42", issue["number"])
	}
	if issue["open"] != true {
		t.Errorf("open = %v, want unredacted true", issue["open"])
	}
}

func TestRedactPayloadRedactsSensitiveKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"exact match", "token"},
		{"api_key", "api_key"},
		{"apikey no separator", "apikey"},
		{"hyphenated", "api-key"},
		{"spaced", "API Key"},
		{"mixed case", "Authorization"},
		{"substring match", "access_token"},
		{"password", "password"},
		{"passwd abbreviation", "passwd"},
		{"private key", "private_key"},
		{"cookie", "cookie"},
		{"credential", "credential"},
		{"access_token", "access_token"},
		{"client_secret", "client_secret"},
		{"signing_key", "signing_key"},
		{"webhook_secret", "webhook_secret"},
		{"x-api-key", "x-api-key"},
		{"x_auth_token", "x_auth_token"},
		{"otp", "otp"},
		{"refresh_token", "refresh_token"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{test.key: "super-secret-value"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := webhookguard.RedactPayload(input)
			if err != nil {
				t.Fatalf("RedactPayload() error = %v", err)
			}
			if string(got) == string(input) {
				t.Fatalf("payload unchanged, want %q redacted", test.key)
			}
			var decoded map[string]any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("redacted output is not valid JSON: %v", err)
			}
			if decoded[test.key] == "super-secret-value" {
				t.Errorf("value under key %q survived redaction", test.key)
			}
		})
	}
}

func TestRedactPayloadRedactsNestedObjectsWholesale(t *testing.T) {
	input := []byte(`{"credentials":{"host":"db.internal","user":"admin"}}`)

	got, err := webhookguard.RedactPayload(input)
	if err != nil {
		t.Fatalf("RedactPayload() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}
	if _, stillObject := decoded["credentials"].(map[string]any); stillObject {
		t.Errorf("credentials still an object, want wholesale redaction: %v", decoded["credentials"])
	}
}

func TestRedactPayloadHandlesArraysAndScalarsAtTopLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"array", `[{"title":"a"},{"title":"b"}]`},
		{"bare string", `"just a string"`},
		{"bare number", `42`},
		{"null", `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := webhookguard.RedactPayload([]byte(test.input)); err != nil {
				t.Errorf("RedactPayload(%s) error = %v, want no error", test.input, err)
			}
		})
	}
}

func TestRedactPayloadRejectsInvalidJSON(t *testing.T) {
	if _, err := webhookguard.RedactPayload([]byte("{not valid json")); err == nil {
		t.Error("RedactPayload() succeeded on invalid JSON, want error")
	}
}

// TestRedactPayloadRedactsSecretValueShapes pins the value-shape heuristic:
// a compact JWT or a PEM private-key block is redacted even when it sits under
// a key the name heuristic would never flag -- the exact "sender names a secret
// field something the heuristic misses" gap docs/prds/webhook-intake-
// security.md point 5 documents as best-effort.
func TestRedactPayloadRedactsSecretValueShapes(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{
			"compact JWT",
			"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		},
		{
			"PEM private key",
			"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA7...\n-----END RSA PRIVATE KEY-----",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Under an innocuous key, not a secret-named one.
			input, err := json.Marshal(map[string]any{"payload": test.value})
			if err != nil {
				t.Fatal(err)
			}
			got, err := webhookguard.RedactPayload(input)
			if err != nil {
				t.Fatalf("RedactPayload() error = %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("redacted output is not valid JSON: %v", err)
			}
			if decoded["payload"] == test.value {
				t.Errorf("secret-shaped value under 'payload' survived redaction")
			}
			if decoded["payload"] != "[redacted]" {
				t.Errorf("decoded payload = %#v, want %q", decoded["payload"], "[redacted]")
			}
		})
	}
}

// TestRedactPayloadKeepsOrdinaryStrings verifies the value-shape heuristic
// does not over-redact ordinary content (the field-mapping use case depends on
// leaving non-secret values intact).
func TestRedactPayloadKeepsOrdinaryStrings(t *testing.T) {
	input := []byte(`{"description":"Fix the build pipeline","buildId":"build-123"}`)
	got, err := webhookguard.RedactPayload(input)
	if err != nil {
		t.Fatalf("RedactPayload() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}
	if decoded["description"] != "Fix the build pipeline" || decoded["buildId"] != "build-123" {
		t.Errorf("ordinary strings were redacted: %#v", decoded)
	}
}
