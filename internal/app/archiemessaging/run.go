package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// Run runs the standalone Messaging Service process until ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	log := slog.Default().With("service", "archie-messaging")

	cfg, err := Resolve(o, log)
	if err != nil {
		return fmt.Errorf("resolve messaging options: %w", err)
	}

	chat, closeGateway, err := gatewayrpc.Dial(cfg.Options.Gateway.Target, cfg.Options.Gateway.Token)
	if err != nil {
		return fmt.Errorf("dial gateway (%s): %w", cfg.Options.Gateway.Target, err)
	}
	defer closeGateway()

	health := newReadinessRegistry(cfg.Options, chat)
	var settings *messaging.SettingsCommand
	var closeStateStore func()
	// channelStatusStore is the same client, held where compose can reach it: the
	// channel report is published to the store this process already dials.
	var channelStatusStore storecontract.ChannelStatusStore
	if cfg.Options.StateStore.Target != "" {
		stateStore, closeClient, dialErr := staterpc.Dial(cfg.Options.StateStore.Target, cfg.Options.StateStore.Token)
		if dialErr != nil {
			return fmt.Errorf("dial state store control plane (%s): %w", cfg.Options.StateStore.Target, dialErr)
		}
		closeStateStore = closeClient
		defer closeStateStore()
		chatSettings, channelVersion, settingsErr := controlplanerpc.NewRPCClient(stateStore.ControlPlane()).RuntimeChatConfig(ctx, config.ChatConfig{
			Telegram: cfg.Telegram, Email: cfg.Email, Webhook: cfg.Webhook, WebhookAddr: cfg.WebhookAddr, ShowToolCalls: cfg.ShowToolCalls,
		})
		if settingsErr != nil {
			return fmt.Errorf("load channel settings: %w", settingsErr)
		}
		reporter := applystatus.New(applystatus.Messaging, stateStore, log)
		reporter.Report(ctx, controlplanerpc.ChannelSettingsKind, channelVersion, nil)
		go reporter.Run(ctx)
		secrets := secret.NewRegistry()
		cfg.Telegram, cfg.Email, cfg.Webhook, cfg.WebhookAddr, cfg.ShowToolCalls = chatSettings.Telegram, chatSettings.Email, chatSettings.Webhook, chatSettings.WebhookAddr, chatSettings.ShowToolCalls
		cfg.TelegramToken, settingsErr = resolveTelegramToken(chatSettings.Telegram, secrets)
		if settingsErr != nil {
			return fmt.Errorf("resolve database telegram token: %w", settingsErr)
		}
		cfg.WebhookSecret, settingsErr = resolveWebhookSecret(chatSettings.Webhook, secrets)
		if settingsErr != nil {
			return fmt.Errorf("resolve database webhook secret: %w", settingsErr)
		}
		settings = messaging.NewSettingsCommand(messagingControlPlane{client: stateStore.ControlPlane()}).WithIdentities(stateStore)
		channelStatusStore = stateStore
	}

	srv, err := compose(ctx, deps{
		ChannelStatus: channelStatusStore,
		Config:        cfg,
		Log:           log,
		Chat:          chat,
		Health:        health,
		Settings:      settings,
	})
	if err != nil {
		return err
	}

	return srv.Start(ctx)
}
