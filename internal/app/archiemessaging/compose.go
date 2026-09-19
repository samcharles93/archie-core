package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/samcharles93/archie-core/internal/channels/email"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/channels/webhook"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

type deps struct {
	Config ResolvedConfig
	Log    *slog.Logger
	Chat   messaging.ChatContract
	Health *health.Registry
}

type channelInstance struct {
	name    string
	gateway gateway.Gateway
	router  *gateway.Router
}

// Service manages the lifecycle of the extracted Messaging Service and its
// channel adapters.
type Service struct {
	cfg      ResolvedConfig
	log      *slog.Logger
	chat     messaging.ChatContract
	health   *health.Registry
	channels []channelInstance

	mu      sync.Mutex
	running bool
	stop    func()
}

func compose(d deps) *Service {
	srv := &Service{
		cfg:    d.Config,
		log:    d.Log,
		chat:   d.Chat,
		health: d.Health,
	}

	// 1. Telegram
	if d.Config.TelegramToken != "" && len(d.Config.Telegram.AllowedUserIDs) > 0 {
		tg := telegram.New(d.Config.TelegramToken, d.Config.Telegram.AllowedUserIDs, d.Log)
		router := NewContractRouter(d.Chat, "telegram")
		srv.channels = append(srv.channels, channelInstance{
			name:    "telegram",
			gateway: tg,
			router:  router,
		})
	}

	// 2. Email
	if d.Config.Email.ListenAddr != "" {
		em := email.New(d.Config.Email.ListenAddr, d.Config.Email.RelayAddr, d.Log)
		router := NewContractRouter(d.Chat, "email")
		srv.channels = append(srv.channels, channelInstance{
			name:    "email",
			gateway: em,
			router:  router,
		})
	}

	// 3. Webhook
	if d.Config.WebhookAddr != "" {
		host, portStr, err := net.SplitHostPort(d.Config.WebhookAddr)
		if err == nil {
			port, _ := strconv.Atoi(portStr)
			wh := webhook.New(host, port, nil, d.Log)
			router := NewContractRouter(d.Chat, "webhook")
			srv.channels = append(srv.channels, channelInstance{
				name:    "webhook",
				gateway: wh,
				router:  router,
			})
		}
	}

	return srv
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
			if err := c.gateway.Start(ctx, c.router, gateway.Lifecycle{}); err != nil && ctx.Err() == nil {
				s.log.Error("channel stopped with error", "name", c.name, "err", err)
			}
		})
	}

	<-ctx.Done()

	for _, ch := range s.channels {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Options.ShutdownTimeout)
		if err := ch.gateway.Stop(shutdownCtx); err != nil {
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
