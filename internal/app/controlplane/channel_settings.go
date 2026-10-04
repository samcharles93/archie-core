package controlplane

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// Aliases for the channel-settings document defined in controlplanerpc.
type (
	channelSettings   = controlplanerpc.ChannelSettings
	telegramSettings  = controlplanerpc.TelegramSettings
	rateLimitSettings = controlplanerpc.RateLimitSettings
	channelDuration   = controlplanerpc.ChannelDuration
)

// ChannelSettingsKind is re-exported so the store-backed side and its callers
// keep naming one kind by one name.
const ChannelSettingsKind = controlplanerpc.ChannelSettingsKind

func seedChannels(cfg config.Config) any {
	chat := cfg.Chat
	return channelSettings{
		Operator: chat.Operator, Workspace: chat.Workspace, UnrestrictedFilesystem: chat.UnrestrictedFilesystem, ShowToolCalls: chat.ShowToolCalls, MaxSteps: chat.MaxSteps, Models: chat.Models,
		Telegram:  telegramSettings{AllowedUserIDs: chat.Telegram.AllowedUserIDs, Token: chat.Telegram.Token, CredentialConfigured: chat.Telegram.Token != (config.SecretRef{})},
		RateLimit: rateLimitSettings{Window: channelDuration(chat.RateLimit.Window), MaxRequests: chat.RateLimit.MaxRequests},
	}
}

func validateChannels(input []byte) error {
	return validateAs(input, func(settings channelSettings) error {
		if settings.MaxSteps < 0 || settings.RateLimit.MaxRequests < 0 || settings.RateLimit.Window < 0 {
			return fmt.Errorf("channel limits must not be negative")
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
