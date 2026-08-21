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
