package controlplane

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// The channel-settings DOCUMENT lives in internal/infrastructure/controlplanerpc,
// beside the client that projects it, so a process that only reads stored
// settings does not link this package's store-backed server and workflow engine
// (archie-core-1ng1). These aliases keep the store-backed side reading the same
// types by the same names it always used, so there is one definition of the
// document rather than a copy on each side of the boundary.
type (
	channelSettings        = controlplanerpc.ChannelSettings
	telegramSettings       = controlplanerpc.TelegramSettings
	webhookChannelSettings = controlplanerpc.WebhookChannelSettings
	emailSettings          = controlplanerpc.EmailSettings
	rateLimitSettings      = controlplanerpc.RateLimitSettings
	channelDuration        = controlplanerpc.ChannelDuration
)

// ChannelSettingsKind is re-exported so the store-backed side and its callers
// keep naming one kind by one name.
const ChannelSettingsKind = controlplanerpc.ChannelSettingsKind

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
