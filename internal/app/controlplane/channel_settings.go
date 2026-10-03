package controlplane

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// Aliases for the channel-settings document defined in controlplanerpc.
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
		Telegram:    telegramSettings{AllowedUserIDs: chat.Telegram.AllowedUserIDs, Token: chat.Telegram.Token, CredentialConfigured: chat.Telegram.Token != (config.SecretRef{})},
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

// normalizeChannels re-encodes the stored document with snake_case keys.
func normalizeChannels(input []byte) ([]byte, error) {
	var settings channelSettings
	if err := json.Unmarshal(input, &settings); err != nil {
		return nil, err
	}
	return json.Marshal(settings)
}
