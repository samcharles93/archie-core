package archiemessaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/messagingrpc"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/sdnotify"
	"github.com/samcharles93/archie-core/internal/secret"
)

type deps struct {
	// ChannelStatus is where the service publishes channel lifecycle for the
	// dashboard. Optional: nil leaves the report in-process only.
	ChannelStatus storecontract.ChannelStatusStore
	// Presence reads which version each service reports for itself, to verify
	// update reports. Nil leaves them unverified.
	Presence storecontract.PresenceStore
	Config   ResolvedConfig
	// ExtensionChannels are channels served by installed channel extensions.
	ExtensionChannels []*channelInstance
	Log               *slog.Logger
	Chat              messaging.ChatContract
	Health            *health.Registry
	Settings          *messaging.SettingsCommand
	// Secrets resolves channel credential references, including those naming an
	// extension engine.
	Secrets *secret.Registry
	// SettingsSource enables live channel-settings reconciliation. Nil disables
	// it.
	SettingsSource chatSettingsSource
	// Updates serves /update from the stored update settings. Nil without a
	// State Store.
	Updates *releaseupdate.Service
	// ApplyReporter records what this process applied. Nil reports nothing.
	ApplyReporter *applystatus.Reporter
	// AppliedVersion is the channel-settings version already running when the
	// service starts, so the first reconcile tick does not re-apply it.
	AppliedVersion int64
	// ReconcileInterval overrides applystatus.RestampInterval, so a test can
	// drive the loop without waiting 30 seconds.
	ReconcileInterval time.Duration
}

// channelInstance is one composed channel and what is needed to restart it.
// mu guards every field below.
type channelInstance struct {
	name    string
	rebuild func(ResolvedConfig) (channels.Channel, error)

	mu             sync.Mutex
	channel        channels.Channel
	cancel         context.CancelFunc
	supervised     bool
	restartPending bool
	// notify wakes a supervisor that is between runs, so an explicit restart
	// starts the replacement instead of waiting out a backoff or a park.
	notify chan struct{}
}

// current returns the channel instance currently serving, under the lock.
func (c *channelInstance) current() channels.Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channel
}

// Service manages the lifecycle of the extracted Messaging Service and its
// channel adapters.
type Service struct {
	// status owns channel lifecycle facts, one entry per composed channel. It is
	// the single writer channels report through (see lifecycleFor), and the
	// producer a dashboard surface reads.
	status *status.Manager
	// statusWriter publishes that report where the dashboard reads it. Nil means
	// this composition has no store to publish to, which is the honest state for a
	// test or a process run without one.
	statusWriter storecontract.ChannelStatusStore
	// publish coalesces a burst of transitions into one write: a reader only ever
	// wants the latest state of every channel.
	publish  chan struct{}
	cfg      ResolvedConfig
	secrets  *secret.Registry
	log      *slog.Logger
	chat     messaging.ChatContract
	health   *health.Registry
	channels []*channelInstance
	// settingsSource re-reads the stored channel settings. Nil disables the
	// reconcile loop.
	settingsSource chatSettingsSource
	// reporter records the applied channel-settings version. Nil reports
	// nothing.
	reporter *applystatus.Reporter
	// appliedVersion is the channel-settings version the running channels were
	// built from, so a reconcile tick does not re-apply its own work.
	appliedVersion atomic.Int64
	// reconcileInterval is how often the stored settings are re-read.
	reconcileInterval time.Duration
	// retry bounds how a supervisor retries a channel that fails on its own
	// before parking it for an explicit restart.
	retry channelRetryPolicy

	mu      sync.Mutex
	running bool
	stop    func()
	// wg tracks the per-channel supervisors, so a shutdown waits for every
	// channel to stop before Start returns. A restart can start a replacement
	// supervisor, which is why it lives here rather than in Start's frame.
	wg sync.WaitGroup
}

// compose builds the Service. ctx is the service lifetime. An invalid
// configured channel fails composition.
func compose(ctx context.Context, d deps) (*Service, error) {
	srv := &Service{
		cfg:               d.Config,
		secrets:           d.Secrets,
		log:               d.Log,
		chat:              d.Chat,
		health:            d.Health,
		settingsSource:    d.SettingsSource,
		reporter:          d.ApplyReporter,
		reconcileInterval: d.ReconcileInterval,
	}
	srv.appliedVersion.Store(d.AppliedVersion)
	if srv.reconcileInterval <= 0 {
		srv.reconcileInterval = applystatus.RestampInterval
	}
	srv.retry = defaultChannelRetry()

	instances, err := composeChannels(ctx, d)
	if err != nil {
		return nil, err
	}
	srv.channels = instances
	srv.status = status.NewManager(channelDescriptors(srv.channels))
	srv.statusWriter = d.ChannelStatus
	srv.publish = make(chan struct{}, 1)
	return srv, nil
}

// composeChannels builds the configured channel set. Each instance carries the
// factory a restart uses to build a replacement from a new resolved
// configuration, which is what makes a channel with no in-place reload seam
// still restarted alone rather than through a process restart.
func composeChannels(ctx context.Context, d deps) ([]*channelInstance, error) {
	instances := make([]*channelInstance, 0, 3)
	if d.Config.TelegramToken != "" {
		instance, err := composeTelegram(ctx, d)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	for _, instance := range d.ExtensionChannels {
		if slices.ContainsFunc(instances, func(have *channelInstance) bool { return have.name == instance.name }) {
			d.Log.Warn("channel extension skipped: a built-in channel has the same name", "channel", instance.name)
			continue
		}
		instances = append(instances, instance)
	}
	for _, instance := range instances {
		instance.notify = make(chan struct{}, 1)
	}
	return instances, nil
}

func composeTelegram(ctx context.Context, d deps) (*channelInstance, error) {
	if len(d.Config.Telegram.AllowedUserIDs) == 0 {
		d.Log.Warn("chat.telegram has no allowed_user_ids: every sender will be rejected. " +
			"Add your Telegram user id to chat.telegram.allowed_user_ids to enable the bot.")
	}
	build := func(cfg ResolvedConfig) (channels.Channel, error) {
		if cfg.TelegramToken == "" {
			return nil, nil
		}
		tg := telegram.New(cfg.TelegramToken, cfg.Telegram.AllowedUserIDs, d.Log)
		tg.Settings = d.Settings
		configureTelegram(ctx, tg, cfg, d)
		if err := tg.ValidateConfig(telegramValidateConfigMap(cfg.Telegram)); err != nil {
			return nil, fmt.Errorf("chat.telegram config invalid: %w", err)
		}
		return tg, nil
	}
	ch, err := build(d.Config)
	if err != nil {
		return nil, err
	}
	return &channelInstance{name: "telegram", channel: ch, rebuild: build}, nil
}

// telegramValidateConfigMap builds the map Gateway.ValidateConfig expects
// from the typed config.
func telegramValidateConfigMap(cfg config.TelegramConfig) map[string]any {
	m := map[string]any{}
	if cfg.Token != (secret.SecretRef{}) {
		m["token"] = map[string]any{"engine": cfg.Token.Engine, "key": cfg.Token.Key}
	}
	return m
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

	if s.statusWriter != nil {
		go s.publishLoop(ctx)
		// Report the composed set before any channel reports itself, so a dashboard
		// opened during startup shows configured channels rather than none.
		s.signalPublish()
	}
	if s.settingsSource != nil {
		go s.reconcileLoop(ctx)
	}

	for _, ch := range s.channels {
		c := ch
		c.mu.Lock()
		c.supervised = true
		c.mu.Unlock()
		s.wg.Add(1)
		go s.runChannel(ctx, c)
	}

	// Notify systemd once every channel has started.
	sdnotify.Ready(s.log)

	<-ctx.Done()

	s.wg.Wait()

	if s.statusWriter != nil {
		// The final report carries the stopped states each supervisor recorded.
		// Its own context, because ctx is already cancelled.
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Options.ShutdownTimeout)
		defer cancel()
		if err := s.publishStatus(finalCtx); err != nil {
			s.log.Warn("publishing final channel status failed", "err", err)
		}
	}
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

// Deliver sends text to chatID through the named running channel.
func (s *Service) Deliver(ctx context.Context, channel, chatID, text string) error {
	for _, instance := range s.channels {
		if instance.name != channel {
			continue
		}
		sender, ok := instance.current().(channels.Sender)
		if !ok {
			return fmt.Errorf("%w: channel %q cannot send", messagingrpc.ErrUnavailable, channel)
		}
		err := sender.Send(ctx, chatID, text)
		if errors.Is(err, channels.ErrNotRunning) {
			return fmt.Errorf("%w: channel %q is not running", messagingrpc.ErrUnavailable, channel)
		}
		return err
	}
	return fmt.Errorf("%w: no channel %q", messagingrpc.ErrUnavailable, channel)
}
