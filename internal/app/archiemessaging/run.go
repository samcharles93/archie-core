package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/messagingrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/secretengine"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
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

	secrets := secret.NewRegistry()
	health := newReadinessRegistry(cfg.Options, chat)
	var settings *messaging.SettingsCommand
	// channelStatusStore is the same client, held where compose can reach it: the
	// channel report is published to the store this process already dials.
	var channelStatusStore storecontract.ChannelStatusStore
	var presenceStore storecontract.PresenceStore
	// settingsSource and applyReporter are the live channel-settings path: the
	// control-plane client to re-read the resource, and the record of what this
	// process applied. Both stay nil without a State Store, which leaves the
	// service with no live path.
	var settingsSource chatSettingsSource
	var applyReporter *applystatus.Reporter
	var appliedVersion int64
	var updates *releaseupdate.Service
	var extensionChannels []*channelInstance
	if cfg.Options.StateStore.Target != "" {
		stateStore, closeClient, dialErr := staterpc.Dial(cfg.Options.StateStore.Target, presence.Messaging, cfg.Options.StateStore.Token, staterpc.WaitForPeer)
		if dialErr != nil {
			return fmt.Errorf("dial state store control plane (%s): %w", cfg.Options.StateStore.Target, dialErr)
		}
		defer closeClient()
		controlPlaneClient := controlplanerpc.NewRPCClient(stateStore.ControlPlane())
		reporter := applystatus.New(applystatus.Messaging, stateStore, log)
		extensions := extension.Source{Query: controlPlaneClient, Packages: stateStore}
		defer superviseSecretEngines(ctx, extensions, secrets, reporter, log)()
		chatSettings, channelVersion, settingsErr := controlPlaneClient.RuntimeChatConfig(ctx, config.ChatConfig{
			Telegram: cfg.Telegram, ShowToolCalls: cfg.ShowToolCalls,
		})
		if settingsErr != nil {
			return fmt.Errorf("load channel settings: %w", settingsErr)
		}
		reporter.Report(ctx, controlplanerpc.ChannelSettingsKind, channelVersion, nil)
		go reporter.Run(ctx)
		go presence.Run(ctx, presence.Messaging, servicekit.Build(), stateStore, health, log)
		cfg.Telegram, cfg.ShowToolCalls = chatSettings.Telegram, chatSettings.ShowToolCalls
		cfg.TelegramToken, settingsErr = resolveTelegramToken(chatSettings.Telegram, secrets)
		if settingsErr != nil {
			return fmt.Errorf("resolve database telegram token: %w", settingsErr)
		}
		extensionChannels = openExtensionChannels(ctx, extensions, secrets, log)
		settings = messaging.NewSettingsCommand(messagingControlPlane{client: stateStore.ControlPlane()}).WithIdentities(stateStore)
		channelStatusStore, presenceStore = stateStore, stateStore
		settingsSource, applyReporter, appliedVersion = controlPlaneClient, reporter, channelVersion
		updates = controlplanerpc.UpdateService(controlPlaneClient, filepath.Join(cfg.WorkDir, "telegram-update-deferrals.json"), cfg.HealthURL)
	}

	if cfg.Options.StateStore.Target == "" {
		if err := cfg.resolveTokens(secrets); err != nil {
			return err
		}
	}
	srv, err := compose(ctx, deps{
		Secrets:           secrets,
		ChannelStatus:     channelStatusStore,
		Presence:          presenceStore,
		Config:            cfg,
		ExtensionChannels: extensionChannels,
		Log:               log,
		Chat:              chat,
		Health:            health,
		Settings:          settings,
		SettingsSource:    settingsSource,
		ApplyReporter:     applyReporter,
		AppliedVersion:    appliedVersion,
		Updates:           updates,
	})
	if err != nil {
		return err
	}
	return serve(ctx, cfg.Options, srv, log)
}

// serve runs srv and serves MessagingService over it until ctx is cancelled.
func serve(ctx context.Context, o Options, srv *Service, log *slog.Logger) error {
	opts, err := messagingrpc.ServerOptions(o.Listen, o.Token)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.Listen)
	if err != nil {
		return fmt.Errorf("listen messaging (%s): %w", o.Listen, err)
	}
	server := grpc.NewServer(opts...)
	messagingrpc.RegisterServer(server, srv)
	go func() {
		if err := server.Serve(listener); err != nil {
			log.Error("messaging gRPC server stopped", "err", err)
		}
	}()
	defer server.GracefulStop()
	log.Info("messaging service listening", "listen", o.Listen)
	return srv.Start(ctx)
}

// superviseSecretEngines runs the secret-engine extensions and reports their
// settings outcome; the returned function stops them.
func superviseSecretEngines(ctx context.Context, source extension.Source, secrets *secret.Registry, reporter *applystatus.Reporter, log *slog.Logger) func() {
	return secretengine.Supervise(ctx, secrets, source, applystatus.Messaging, func(ctx context.Context, version int64, err error) {
		reporter.Report(ctx, controlplanerpc.ExtensionSettingsKind, version, err)
	}, log).Close
}
