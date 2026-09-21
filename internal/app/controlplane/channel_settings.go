package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

// channelSettings is the Channel settings resource document. Its shape IS the
// contract the Web UI edits, so every nested object here is defined by this
// file with explicit snake_case json tags and never handed an internal config
// struct. Encoding/json falls back to Go field names when a tag is absent,
// which is how "Window", "MaxRequests", "ListenAddr" and "RelayAddr" reached
// operators in the first place -- a shape belonging to TOML, not to this
// document.
type channelSettings struct {
	Operator               string                 `json:"operator"`
	ShowToolCalls          bool                   `json:"show_tool_calls"`
	MaxSteps               int                    `json:"max_steps"`
	Models                 []string               `json:"models"`
	Email                  emailSettings          `json:"email"`
	WebhookAddr            string                 `json:"webhook_addr"`
	Webhook                webhookChannelSettings `json:"webhook"`
	Telegram               telegramSettings       `json:"telegram"`
	RateLimit              rateLimitSettings      `json:"rate_limit"`
	UnrestrictedFilesystem bool                   `json:"unrestricted_filesystem"`
	Workspace              string                 `json:"workspace"`
}

type telegramSettings struct {
	AllowedUserIDs       []int64          `json:"allowed_user_ids"`
	Token                config.SecretRef `json:"token_ref"`
	TokenEnv             string           `json:"token_env,omitempty"`
	CredentialConfigured bool             `json:"credential_configured"`
}

type webhookChannelSettings struct {
	Path                 string           `json:"path"`
	Secret               config.SecretRef `json:"secret_ref"`
	CredentialConfigured bool             `json:"credential_configured"`
	Template             string           `json:"template"`
	DeliverTo            string           `json:"deliver_to"`
}

// emailSettings is the email object. It reads the Go-cased keys an earlier
// revision stored as well as its own, because json tags alone would not: the
// decoder's case-insensitive fallback compares whole keys, so "ListenAddr"
// never matches "listen_addr".
type emailSettings struct {
	ListenAddr string `json:"listen_addr"`
	RelayAddr  string `json:"relay_addr"`
}

func (e *emailSettings) UnmarshalJSON(data []byte) error {
	type document emailSettings
	var decoded document
	err := decodeRenamingLegacyKeys(data, map[string]string{
		"ListenAddr": "listen_addr",
		"RelayAddr":  "relay_addr",
	}, &decoded)
	if err != nil {
		return err
	}
	*e = emailSettings(decoded)
	return nil
}

// rateLimitSettings is the inbound rate-limit object, and it reads the Go-cased
// keys for the reason emailSettings does.
type rateLimitSettings struct {
	Window      channelDuration `json:"window"`
	MaxRequests int             `json:"max_requests"`
}

func (r *rateLimitSettings) UnmarshalJSON(data []byte) error {
	type document rateLimitSettings
	var decoded document
	err := decodeRenamingLegacyKeys(data, map[string]string{
		"Window":      "window",
		"MaxRequests": "max_requests",
	}, &decoded)
	if err != nil {
		return err
	}
	*r = rateLimitSettings(decoded)
	return nil
}

// decodeRenamingLegacyKeys decodes an object into v after renaming each key in
// legacy to the spelling v documents, and stays strict about everything else.
// The strictness is the point: dropping unknown keys would let a typo
// ("windows") read as "unset" and silently disable the setting, which is the
// class of failure this shape exists to end.
func decodeRenamingLegacyKeys(data []byte, legacy map[string]string, v any) error {
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

// channelDuration is a duration as this document writes it: the human string
// the file accepts ("1m0s", the same spelling config.Duration emits), never a
// nanosecond count. It still reads the count, because every document written
// before this shape existed carries one, and reading both forms is what lets
// those documents keep working without being re-saved.
type channelDuration time.Duration

// Std is the standard-library duration, for arithmetic and for the marker
// schema.go uses to recognise a string-form duration (see durationLike).
func (d channelDuration) Std() time.Duration { return time.Duration(d) }

func (d channelDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *channelDuration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("duration must be a Go duration string such as %q: %w", "1m0s", err)
		}
		*d = channelDuration(value)
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return fmt.Errorf("duration must be a Go duration string such as %q or a nanosecond count: %w", "1m0s", err)
	}
	*d = channelDuration(nanos)
	return nil
}

func seedChannels(cfg config.Config) any {
	chat := cfg.Chat
	return channelSettings{
		Operator: chat.Operator, Workspace: chat.Workspace, UnrestrictedFilesystem: chat.UnrestrictedFilesystem, ShowToolCalls: chat.ShowToolCalls, MaxSteps: chat.MaxSteps, Models: chat.Models,
		Email:       emailSettings{ListenAddr: chat.Email.ListenAddr, RelayAddr: chat.Email.RelayAddr},
		WebhookAddr: chat.WebhookAddr,
		Telegram:    telegramSettings{AllowedUserIDs: chat.Telegram.AllowedUserIDs, Token: chat.Telegram.Token, TokenEnv: chat.Telegram.TokenEnv, CredentialConfigured: chat.Telegram.TokenEnv != "" || chat.Telegram.Token != (config.SecretRef{})},
		Webhook:     webhookChannelSettings{Path: chat.Webhook.Path, Secret: chat.Webhook.Secret, CredentialConfigured: chat.Webhook.Secret != (config.SecretRef{}), Template: chat.Webhook.Template, DeliverTo: chat.Webhook.DeliverTo},
		RateLimit:   rateLimitSettings{Window: channelDuration(chat.RateLimit.Window), MaxRequests: chat.RateLimit.MaxRequests},
	}
}

func validateChannels(input []byte) error {
	return validateAs(input, func(settings channelSettings) error {
		if settings.MaxSteps < 0 || settings.RateLimit.MaxRequests < 0 || settings.RateLimit.Window < 0 {
			return fmt.Errorf("channel limits must not be negative")
		}
		if settings.Webhook.DeliverTo != "" && settings.Webhook.DeliverTo != "origin" {
			return fmt.Errorf("webhook deliver_to must be empty or origin")
		}
		return nil
	})
}

// normalizeChannels canonicalizes the stored document: decoding into the shape
// above and re-encoding writes the snake_case keys and the string duration,
// dropping the Go-cased keys an earlier revision stored. A document that still
// carries them converges the next time it is written; reads tolerate them
// meanwhile (emailSettings, rateLimitSettings), so nothing has to be re-saved
// before it can be read again.
func normalizeChannels(input []byte) ([]byte, error) {
	var settings channelSettings
	if err := json.Unmarshal(input, &settings); err != nil {
		return nil, err
	}
	return json.Marshal(settings)
}
