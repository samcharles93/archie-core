package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

// channelSettingsDefinition returns the registered definition, so a test that
// pins the document's shape also pins its wiring: Seed, Validate and Normalize
// are the three functions the registry actually runs.
func channelSettingsDefinition(t *testing.T) Definition {
	t.Helper()
	for _, definition := range operationalDefinitions() {
		if definition.Kind == ChannelSettingsKind {
			return definition
		}
	}
	t.Fatal("channel-settings is not registered")
	return Definition{}
}

// TestChannelSettingsSeedIsTheDocumentShape pins what an operator sees in the
// Web UI: snake_case keys and a duration they can read. The Go-cased keys and
// the nanosecond count this replaced are asserted absent, because the defect was
// invisible to a test that only compared decoded values -- encoding/json
// round-trips the wrong spelling perfectly happily.
func TestChannelSettingsSeedIsTheDocumentShape(t *testing.T) {
	cfg := config.Config{Chat: config.ChatConfig{
		Email:     config.EmailConfig{ListenAddr: "127.0.0.1:2525", RelayAddr: "smtp.example.com:587"},
		RateLimit: config.RateLimitConfig{Window: time.Minute, MaxRequests: 20},
	}}
	encoded, err := json.Marshal(seedChannels(cfg))
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	rateLimit, ok := document["rate_limit"].(map[string]any)
	if !ok {
		t.Fatalf("rate_limit = %#v, want an object", document["rate_limit"])
	}
	if got := rateLimit["window"]; got != "1m0s" {
		t.Errorf("rate_limit.window = %#v, want %q: the document writes the string the file accepts", got, "1m0s")
	}
	if got := rateLimit["max_requests"]; got != float64(20) {
		t.Errorf("rate_limit.max_requests = %#v, want 20", got)
	}
	email, ok := document["email"].(map[string]any)
	if !ok {
		t.Fatalf("email = %#v, want an object", document["email"])
	}
	if email["listen_addr"] != "127.0.0.1:2525" || email["relay_addr"] != "smtp.example.com:587" {
		t.Errorf("email = %#v, want the snake_case addresses", email)
	}
	for _, legacy := range []string{"Window", "MaxRequests", "ListenAddr", "RelayAddr"} {
		if _, present := email[legacy]; present {
			t.Errorf("email carries the Go-cased key %q", legacy)
		}
		if _, present := rateLimit[legacy]; present {
			t.Errorf("rate_limit carries the Go-cased key %q", legacy)
		}
	}
}

// legacyChannelDocument is a document as an earlier revision stored it: the Go
// field names encoding/json fell back to, and a nanosecond count where the
// document now writes a string. Every store written before the reshape holds
// this, so it has to keep working.
const legacyChannelDocument = `{"rate_limit":{"Window":60000000000,"MaxRequests":20},"email":{"ListenAddr":"127.0.0.1:2525","RelayAddr":"smtp.example.com:587"}}`

// TestChannelSettingsReadsADocumentWrittenBeforeTheReshape covers the two halves
// the change needs: a legacy document still validates and still reads, and
// writing it back (Definition.Decode is what every write path runs) rewrites it
// into the current shape.
func TestChannelSettingsReadsADocumentWrittenBeforeTheReshape(t *testing.T) {
	definition := channelSettingsDefinition(t)

	if err := definition.Validate([]byte(legacyChannelDocument)); err != nil {
		t.Fatalf("Validate(legacy) = %v, want it accepted: it is what existing stores hold", err)
	}

	decoded, err := definition.Decode([]byte(legacyChannelDocument))
	if err != nil {
		t.Fatalf("Decode(legacy) = %v", err)
	}
	if strings.Contains(string(decoded), `"Window"`) || strings.Contains(string(decoded), `60000000000`) {
		t.Fatalf("decoded document = %s, want the canonical keys and duration", decoded)
	}
	var document struct {
		RateLimit struct {
			Window      string `json:"window"`
			MaxRequests int    `json:"max_requests"`
		} `json:"rate_limit"`
		Email struct {
			ListenAddr string `json:"listen_addr"`
			RelayAddr  string `json:"relay_addr"`
		} `json:"email"`
	}
	if err := json.Unmarshal(decoded, &document); err != nil {
		t.Fatalf("unmarshal decoded document: %v", err)
	}
	if document.RateLimit.Window != "1m0s" || document.RateLimit.MaxRequests != 20 {
		t.Errorf("rate_limit = %+v, want the legacy values preserved in the current shape", document.RateLimit)
	}
	if document.Email.ListenAddr != "127.0.0.1:2525" || document.Email.RelayAddr != "smtp.example.com:587" {
		t.Errorf("email = %+v, want the legacy values preserved", document.Email)
	}
}

// TestChannelSettingsLayersEveryStoredFormOfTheDuration walks the forms a stored
// document may hold: the legacy Go-cased keys with a nanosecond count, the
// current keys with the string the file writes, and the current keys with a
// count. All three have to reach config.ChatConfig, or a deployment's rate limit
// silently stops applying.
func TestChannelSettingsLayersEveryStoredFormOfTheDuration(t *testing.T) {
	for _, tt := range []struct {
		name     string
		rate     map[string]any
		expected time.Duration
	}{
		{
			name:     "legacy keys, nanosecond count",
			rate:     map[string]any{"Window": int64(60000000000), "MaxRequests": 20},
			expected: time.Minute,
		},
		{
			name:     "current keys, duration string",
			rate:     map[string]any{"window": "1m", "max_requests": 20},
			expected: time.Minute,
		},
		{
			name:     "current keys, nanosecond count",
			rate:     map[string]any{"window": int64(60000000000), "max_requests": 20},
			expected: time.Minute,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
				ChannelSettingsKind: map[string]any{
					"rate_limit": tt.rate,
					"email":      map[string]any{"ListenAddr": "127.0.0.1:2525", "RelayAddr": "smtp.example.com:587"},
				},
			}})
			got, version, err := client.RuntimeChatConfig(t.Context(), config.ChatConfig{})
			if err != nil {
				t.Fatalf("RuntimeChatConfig: %v", err)
			}
			if version != 2 {
				t.Errorf("version = %d, want the version the store answered with", version)
			}
			if got.RateLimit.Window != tt.expected || got.RateLimit.MaxRequests != 20 || !got.RateLimit.Enabled() {
				t.Errorf("RateLimit = %+v, want window %v and 20 requests", got.RateLimit, tt.expected)
			}
			if got.Email.ListenAddr != "127.0.0.1:2525" || got.Email.RelayAddr != "smtp.example.com:587" {
				t.Errorf("Email = %+v, want the stored addresses", got.Email)
			}
		})
	}
}

// TestChannelSettingsRefusesWhatItCannotApply keeps the tolerance above from
// becoming indifference. Reading a legacy spelling is deliberate; quietly
// swallowing a misspelling is the failure this whole shape exists to prevent,
// because an unknown key decodes as an unset one and an unset rate limit is
// off.
func TestChannelSettingsRefusesWhatItCannotApply(t *testing.T) {
	definition := channelSettingsDefinition(t)
	for _, tt := range []struct{ name, document string }{
		{"a misspelt key is not an absent setting", `{"rate_limit":{"windows":"1m","max_requests":20}}`},
		{"an unknown top-level key", `{"rate_limmt":{}}`},
		{"an unparseable duration", `{"rate_limit":{"window":"soon","max_requests":20}}`},
		{"a fractional duration", `{"rate_limit":{"window":1.5,"max_requests":20}}`},
		{"a negative budget", `{"rate_limit":{"window":"1m","max_requests":-1}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := definition.Validate([]byte(tt.document)); err == nil {
				t.Fatalf("Validate(%s) = nil, want a refusal: boot fails closed on a document it cannot apply", tt.document)
			}
		})
	}
}
