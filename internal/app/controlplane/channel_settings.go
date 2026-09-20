package controlplane

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
)

type channelSettings struct {
	Operator               string                 `json:"operator"`
	ShowToolCalls          bool                   `json:"show_tool_calls"`
	MaxSteps               int                    `json:"max_steps"`
	Models                 []string               `json:"models"`
	Email                  config.EmailConfig     `json:"email"`
	WebhookAddr            string                 `json:"webhook_addr"`
	Webhook                webhookChannelSettings `json:"webhook"`
	Telegram               telegramSettings       `json:"telegram"`
	RateLimit              config.RateLimitConfig `json:"rate_limit"`
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

func seedChannels(cfg config.Config) any {
	chat := cfg.Chat
	return channelSettings{
		Operator: chat.Operator, Workspace: chat.Workspace, UnrestrictedFilesystem: chat.UnrestrictedFilesystem, ShowToolCalls: chat.ShowToolCalls, MaxSteps: chat.MaxSteps, Models: chat.Models, Email: chat.Email, WebhookAddr: chat.WebhookAddr,
		Telegram: telegramSettings{AllowedUserIDs: chat.Telegram.AllowedUserIDs, Token: chat.Telegram.Token, TokenEnv: chat.Telegram.TokenEnv, CredentialConfigured: chat.Telegram.TokenEnv != "" || chat.Telegram.Token != (config.SecretRef{})},
		Webhook:  webhookChannelSettings{Path: chat.Webhook.Path, Secret: chat.Webhook.Secret, CredentialConfigured: chat.Webhook.Secret != (config.SecretRef{}), Template: chat.Webhook.Template, DeliverTo: chat.Webhook.DeliverTo}, RateLimit: chat.RateLimit,
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
