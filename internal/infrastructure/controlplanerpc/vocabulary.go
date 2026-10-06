package controlplanerpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// ChannelSettings is the channel-settings resource document, with
// snake_case JSON keys.
type ChannelSettings struct {
	Operator               string            `json:"operator"`
	ShowToolCalls          bool              `json:"show_tool_calls"`
	MaxSteps               int               `json:"max_steps"`
	Models                 []string          `json:"models"`
	Telegram               TelegramSettings  `json:"telegram"`
	RateLimit              RateLimitSettings `json:"rate_limit"`
	UnrestrictedFilesystem bool              `json:"unrestricted_filesystem"`
	Workspace              string            `json:"workspace"`
}

type TelegramSettings struct {
	AllowedUserIDs       []int64          `json:"allowed_user_ids"`
	Token                config.SecretRef `json:"token_ref"`
	CredentialConfigured bool             `json:"credential_configured"`
}

// RateLimitSettings is the inbound rate-limit object, and it reads the Go-cased
// keys: the decoder's case-insensitive fallback compares whole keys, so "MaxRequests"
// never matches "max_requests".
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

// DecodeRenamingLegacyKeys decodes data into v after renaming legacy keys.
// Unknown keys are errors.
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

// ChannelDuration is written as a duration string; it also reads a
// nanosecond count.
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

// UpdateSettingsKind holds which releases the update scripts select.
const UpdateSettingsKind = "update-settings"

// UpdateSettings is the update-settings document.
type UpdateSettings struct {
	Channel string `json:"channel" title:"Release channel" doc:"stable, next (includes prereleases) or exact-pin."`
	Pin     string `json:"pin" title:"Pinned version" doc:"The version exact-pin installs."`
}

// UpdateService checks GitHub on the stored release channel and installs
// through the script beside this binary. statePath keeps each recipient's
// deferrals; healthURL reaches the installer.
func UpdateService(reader ResourceReader, statePath, healthURL string) *releaseupdate.Service {
	return &releaseupdate.Service{
		Catalog:     releaseupdate.GitHubCatalog{Channel: updateChannel(reader)},
		Installer:   releaseupdate.ScriptInstaller{HealthURL: healthURL},
		StatePath:   statePath,
		InstallType: installtype.Type(),
	}
}

// updateChannel reads the update-settings resource. An unstored kind is the
// stable channel.
func updateChannel(reader ResourceReader) func(context.Context) (string, string, error) {
	return func(ctx context.Context) (string, string, error) {
		settings := UpdateSettings{Channel: "stable"}
		_, _, err := reader.Query(ctx, UpdateSettingsKind, func(value []byte) error {
			return json.Unmarshal(value, &settings)
		})
		return settings.Channel, settings.Pin, err
	}
}
