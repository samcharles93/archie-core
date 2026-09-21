package controlplanerpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

// ChannelSettings is the Channel settings resource document. Its shape IS the
// contract the Web UI edits, so every nested object here is defined by this
// file with explicit snake_case json tags and never handed an internal config
// struct. Encoding/json falls back to Go field names when a tag is absent,
// which is how "Window", "MaxRequests", "ListenAddr" and "RelayAddr" reached
// operators in the first place -- a shape belonging to TOML, not to this
// document.
type ChannelSettings struct {
	Operator               string                 `json:"operator"`
	ShowToolCalls          bool                   `json:"show_tool_calls"`
	MaxSteps               int                    `json:"max_steps"`
	Models                 []string               `json:"models"`
	Email                  EmailSettings          `json:"email"`
	WebhookAddr            string                 `json:"webhook_addr"`
	Webhook                WebhookChannelSettings `json:"webhook"`
	Telegram               TelegramSettings       `json:"telegram"`
	RateLimit              RateLimitSettings      `json:"rate_limit"`
	UnrestrictedFilesystem bool                   `json:"unrestricted_filesystem"`
	Workspace              string                 `json:"workspace"`
}

type TelegramSettings struct {
	AllowedUserIDs       []int64          `json:"allowed_user_ids"`
	Token                config.SecretRef `json:"token_ref"`
	TokenEnv             string           `json:"token_env,omitempty"`
	CredentialConfigured bool             `json:"credential_configured"`
}

type WebhookChannelSettings struct {
	Path                 string           `json:"path"`
	Secret               config.SecretRef `json:"secret_ref"`
	CredentialConfigured bool             `json:"credential_configured"`
	Template             string           `json:"template"`
	DeliverTo            string           `json:"deliver_to"`
}

// EmailSettings is the email object. It reads the Go-cased keys an earlier
// revision stored as well as its own, because json tags alone would not: the
// decoder's case-insensitive fallback compares whole keys, so "ListenAddr"
// never matches "listen_addr".
type EmailSettings struct {
	ListenAddr string `json:"listen_addr"`
	RelayAddr  string `json:"relay_addr"`
}

func (e *EmailSettings) UnmarshalJSON(data []byte) error {
	type document EmailSettings
	var decoded document
	err := DecodeRenamingLegacyKeys(data, map[string]string{
		"ListenAddr": "listen_addr",
		"RelayAddr":  "relay_addr",
	}, &decoded)
	if err != nil {
		return err
	}
	*e = EmailSettings(decoded)
	return nil
}

// RateLimitSettings is the inbound rate-limit object, and it reads the Go-cased
// keys for the reason EmailSettings does.
type RateLimitSettings struct {
	Window      ChannelDuration `json:"window"`
	MaxRequests int             `json:"max_requests"`
}

func (r *RateLimitSettings) UnmarshalJSON(data []byte) error {
	type document RateLimitSettings
	var decoded document
	err := DecodeRenamingLegacyKeys(data, map[string]string{
		"Window":      "window",
		"MaxRequests": "max_requests",
	}, &decoded)
	if err != nil {
		return err
	}
	*r = RateLimitSettings(decoded)
	return nil
}

// DecodeRenamingLegacyKeys decodes an object into v after renaming each key in
// legacy to the spelling v documents, and stays strict about everything else.
// The strictness is the point: dropping unknown keys would let a typo
// ("windows") read as "unset" and silently disable the setting, which is the
// class of failure this shape exists to end.
func DecodeRenamingLegacyKeys(data []byte, legacy map[string]string, v any) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	canonical := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if renamed, ok := legacy[key]; ok {
			key = renamed
		}
		if _, duplicate := canonical[key]; duplicate {
			return fmt.Errorf("duplicate key %q", key)
		}
		canonical[key] = value
	}
	renamed, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(renamed))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

// ChannelDuration is a duration as this document writes it: the human string
// the file accepts ("1m0s", the same spelling config.Duration emits), never a
// nanosecond count. It still reads the count, because every document written
// before this shape existed carries one, and reading both forms is what lets
// those documents keep working without being re-saved.
type ChannelDuration time.Duration

// Std is the standard-library duration, for arithmetic and for the marker
// schema.go uses to recognise a string-form duration (see durationLike).
func (d ChannelDuration) Std() time.Duration { return time.Duration(d) }

func (d ChannelDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *ChannelDuration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("duration must be a Go duration string such as %q: %w", "1m0s", err)
		}
		*d = ChannelDuration(value)
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return fmt.Errorf("duration must be a Go duration string such as %q or a nanosecond count: %w", "1m0s", err)
	}
	*d = ChannelDuration(nanos)
	return nil
}
