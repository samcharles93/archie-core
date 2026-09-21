package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/email"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/channels/webhook"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/secret"
)

type deps struct {
	Config   ResolvedConfig
	Log      *slog.Logger
	Chat     messaging.ChatContract
	Health   *health.Registry
	Settings *messaging.SettingsCommand
}

type channelInstance struct {
	name    string
	channel channels.Channel
}

// Service manages the lifecycle of the extracted Messaging Service and its
// channel adapters.
type Service struct {
	// status owns channel lifecycle facts, one entry per composed channel. It is
	// the single writer channels report through (see lifecycleFor), and the
	// producer a dashboard surface reads.
	status   *status.Manager
	cfg      ResolvedConfig
	log      *slog.Logger
	chat     messaging.ChatContract
	health   *health.Registry
	channels []channelInstance

	mu      sync.Mutex
	running bool
	stop    func()
}

// compose builds the Service from resolved configuration. ctx is the service
// lifetime: seams a channel invokes later (the Gateway version lookup) derive
// their calls from it, so a shutdown cancels them. A channel whose
// configuration is present but invalid fails composition rather than being
// dropped: a front-end the operator configured must never silently not run.
func compose(ctx context.Context, d deps) (*Service, error) {
	srv := &Service{
		cfg:    d.Config,
		log:    d.Log,
		chat:   d.Chat,
		health: d.Health,
	}

	if d.Config.TelegramToken != "" {
		if len(d.Config.Telegram.AllowedUserIDs) == 0 {
			d.Log.Warn("chat.telegram has no allowed_user_ids: every sender will be rejected. " +
				"Add your Telegram user id to chat.telegram.allowed_user_ids to enable the bot.")
		}
		tg := telegram.New(d.Config.TelegramToken, d.Config.Telegram.AllowedUserIDs, d.Log)
		tg.Settings = d.Settings
		configureTelegram(ctx, tg, d.Config, d.Chat, d.Log)
		if err := srv.add("telegram", tg, telegramValidateConfigMap(d.Config.Telegram)); err != nil {
			return nil, err
		}
	}

	if d.Config.Email.ListenAddr != "" {
		em := email.New(d.Config.Email.ListenAddr, d.Config.Email.RelayAddr, d.Log)
		if err := srv.add("email", em, map[string]any{
			"listen_addr": d.Config.Email.ListenAddr,
			"relay_addr":  d.Config.Email.RelayAddr,
		}); err != nil {
			return nil, err
		}
	}

	if d.Config.WebhookAddr != "" {
		host, port := parseListenAddr(d.Config.WebhookAddr, "0.0.0.0", 8644)
		wh := webhook.New(host, port, webhookRoutes(d.Config.Webhook, d.Config.WebhookSecret), d.Log)
		if err := srv.add("webhook", wh, map[string]any{"host": host, "port": port}); err != nil {
			return nil, err
		}
	}

	srv.status = status.NewManager(channelDescriptors(srv.channels))
	return srv, nil
}

// add validates a channel against its own ConfigSchema contract and
// registers it.
func (s *Service) add(name string, ch channels.Channel, cfg map[string]any) error {
	if err := ch.ValidateConfig(cfg); err != nil {
		return fmt.Errorf("chat.%s config invalid: %w", name, err)
	}
	s.channels = append(s.channels, channelInstance{name: name, channel: ch})
	return nil
}

// telegramValidateConfigMap builds the map Gateway.ValidateConfig expects
// from the typed config, carrying whichever credential source is set
// (token takes precedence, matching resolveTelegramToken).
func telegramValidateConfigMap(cfg config.TelegramConfig) map[string]any {
	m := map[string]any{"token_env": cfg.TokenEnv}
	if cfg.Token != (secret.SecretRef{}) {
		m["token"] = map[string]any{"engine": cfg.Token.Engine, "key": cfg.Token.Key}
	}
	return m
}

func webhookRoutes(route config.WebhookRoute, secretValue string) []webhook.RouteConfig {
	path := route.Path
	if path == "" {
		path = "/webhook"
	}
	return []webhook.RouteConfig{{
		Path:      path,
		Secret:    secretValue,
		Template:  route.Template,
		DeliverTo: route.DeliverTo,
	}}
}

func parseListenAddr(addr, defaultHost string, defaultPort int) (string, int) {
	if addr == "" {
		return defaultHost, defaultPort
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return defaultHost, defaultPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return defaultHost, defaultPort
	}
	return host, port
}

// Start launches the configured messaging channels.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("messaging service is already running")
	}
	s.running = true
	ctx, cancel := context.WithCancel(ctx)
	s.stop = cancel
	s.mu.Unlock()

	s.log.Info("messaging service started", "gateway_target", s.cfg.Options.Gateway.Target, "channels", len(s.channels))

	var wg sync.WaitGroup
	for _, ch := range s.channels {
		c := ch
		wg.Go(func() {
			s.log.Info("starting channel", "name", c.name)
			if err := c.channel.Start(ctx, s.chat, s.lifecycleFor(c.name)); err != nil && ctx.Err() == nil {
				// The channel could not run: say so on the operator's surface, not
				// only in the log, which is the difference between a dashboard that
				// shows a dead channel and one that shows nothing at all.
				s.status.MarkFailed(c.name, err.Error())
				s.log.Error("channel stopped with error", "name", c.name, "err", err)
				return
			}
			// A channel that returns without an error has stopped, either because
			// the service was asked to or because its own loop ended.
			s.status.MarkStopped(c.name, "")
		})
	}

	<-ctx.Done()

	for _, ch := range s.channels {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Options.ShutdownTimeout)
		if err := ch.channel.Stop(shutdownCtx); err != nil {
			s.log.Warn("channel stop failed", "name", ch.name, "err", err)
		}
		shutdownCancel()
	}

	wg.Wait()
	return nil
}

// Stop stops the messaging service and its channels.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	if s.stop != nil {
		s.stop()
	}
	s.log.Info("messaging service stopped")
}
