package archiemessaging

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/secret"
)

// servingChannel reports itself running once it starts, which is what an
// enable is reported active on: a channel that only reaches "starting" is not
// serving yet.
type servingChannel struct {
	mu     sync.Mutex
	starts int
}

func (c *servingChannel) Name() string { return "telegram" }

func (c *servingChannel) Start(ctx context.Context, _ messaging.ChatContract, lifecycle channels.Lifecycle) error {
	c.mu.Lock()
	c.starts++
	c.mu.Unlock()
	if lifecycle.Starting != nil {
		lifecycle.Starting()
	}
	if lifecycle.Running != nil {
		lifecycle.Running()
	}
	<-ctx.Done()
	return nil
}

func (c *servingChannel) Stop(context.Context) error { return nil }

func (c *servingChannel) ConfigSchema() json.RawMessage { return nil }

func (c *servingChannel) ValidateConfig(map[string]any) error { return nil }

func (c *servingChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts
}

// waitCount blocks until count reaches want, so an async start is observed
// rather than slept for.
func waitCount(t *testing.T, count func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if count() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("channel started %d times, want at least %d", count(), want)
}

// startEnablementService composes and starts a service with the Telegram
// channel's factory replaced by a scripted channel, so enablement can be driven
// without a real bot.
func startEnablementService(t *testing.T, ch *servingChannel) *Service {
	t.Helper()
	srv, err := compose(context.Background(), deps{Log: slog.Default()})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	inst := srv.instanceByID("telegram")
	if inst == nil {
		t.Fatal("telegram channel was not composed")
	}
	inst.rebuild = func(ResolvedConfig) (channels.Channel, error) { return ch, nil }
	srv.retry = channelRetryPolicy{attempts: 3, backoff: time.Millisecond, maxBackoff: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("service did not stop after cancel")
		}
	})
	waitSupervised(t, srv, "telegram")
	return srv
}

// waitSupervised blocks until the channel's supervisor exists, so an enable
// issued right after Start is not refused for racing it.
func waitSupervised(t *testing.T, srv *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		inst := srv.instanceByID(id)
		inst.mu.Lock()
		supervised := inst.supervised
		inst.mu.Unlock()
		if supervised {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("channel %q was never supervised", id)
}

func channelByID(t *testing.T, srv *Service, id string) status.Status {
	t.Helper()
	for _, st := range srv.ChannelStatus() {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("no channel %q in status", id)
	return status.Status{}
}

// TestChannelEnablementAppliesLive pins that enabling and disabling a channel
// starts and stops it without a service restart, and that its declaration
// follows: a channel that was never configured is not reported as configured,
// and one that enablement stopped is reported stopped and unconfigured.
func TestChannelEnablementAppliesLive(t *testing.T) {
	ch := &servingChannel{}
	srv := startEnablementService(t, ch)

	if before := channelByID(t, srv, "telegram"); before.Configured || before.State != status.StateStopped {
		t.Fatalf("telegram before enable = %+v, want not configured and stopped", before)
	}

	if err := srv.enableChannel("telegram", ResolvedConfig{TelegramToken: "token"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitCount(t, ch.count, 1)
	if enabled := channelByID(t, srv, "telegram"); !enabled.Configured || enabled.State != status.StateRunning {
		t.Fatalf("telegram after enable = %+v, want configured and running", enabled)
	}

	if err := srv.disableChannel("telegram"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled := channelByID(t, srv, "telegram"); disabled.Configured || disabled.State != status.StateStopped {
		t.Fatalf("telegram after disable = %+v, want not configured and stopped", disabled)
	}
}

// fakeSettings serves a stored chat config at a version, the way the
// control-plane client does.
type fakeSettings struct {
	chat    config.ChatConfig
	version int64
}

func (f fakeSettings) RuntimeChatConfig(context.Context, config.ChatConfig) (config.ChatConfig, int64, error) {
	return f.chat, f.version, nil
}

// fakeEngine resolves every key, so a stored token reference is resolvable.
type fakeEngine struct{}

func (fakeEngine) Name() string                       { return "test" }
func (fakeEngine) Resolve(key string) (string, error) { return "token-" + key, nil }

// TestReconcileOnceEnablesAndDisables drives the whole stored-settings path:
// a settings version that configures a token enables the channel and is
// recorded as applied only once the channel is running it; a later version
// that clears the token disables it.
func TestReconcileOnceEnablesAndDisables(t *testing.T) {
	ch := &servingChannel{}
	srv := startEnablementService(t, ch)
	srv.secrets = secret.NewRegistry()
	srv.secrets.Register(fakeEngine{})

	srv.settingsSource = fakeSettings{
		chat:    config.ChatConfig{Telegram: config.TelegramConfig{Token: secret.SecretRef{Engine: "test", Key: "bot"}}},
		version: 1,
	}
	if err := srv.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile enable: %v", err)
	}
	waitCount(t, ch.count, 1)
	if got := channelByID(t, srv, "telegram"); !got.Configured {
		t.Fatalf("telegram not enabled by reconcile: %+v", got)
	}
	if srv.appliedVersion.Load() != 1 {
		t.Fatalf("appliedVersion = %d after enable, want 1", srv.appliedVersion.Load())
	}

	srv.settingsSource = fakeSettings{version: 2}
	if err := srv.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile disable: %v", err)
	}
	if got := channelByID(t, srv, "telegram"); got.Configured || got.State != status.StateStopped {
		t.Fatalf("telegram not disabled by reconcile: %+v", got)
	}
	if srv.appliedVersion.Load() != 2 {
		t.Fatalf("appliedVersion = %d after disable, want 2", srv.appliedVersion.Load())
	}
}

// TestApplyChannelSettingsEnablesAndDisables pins the reconcile entry point:
// a settings change that turns a channel on or off starts or stops it and
// reports applied, and a tick that changes nothing is a no-op.
func TestApplyChannelSettingsEnablesAndDisables(t *testing.T) {
	ch := &servingChannel{}
	srv := startEnablementService(t, ch)

	applied, err := srv.applyChannelSettings(ResolvedConfig{TelegramToken: "token"})
	if err != nil || !applied {
		t.Fatalf("enable: applied=%v err=%v", applied, err)
	}
	waitCount(t, ch.count, 1)
	if got := channelByID(t, srv, "telegram"); !got.Configured || got.State == status.StateStopped {
		t.Fatalf("telegram not enabled: %+v", got)
	}

	applied, err = srv.applyChannelSettings(ResolvedConfig{})
	if err != nil || !applied {
		t.Fatalf("disable: applied=%v err=%v", applied, err)
	}
	if got := channelByID(t, srv, "telegram"); got.Configured || got.State != status.StateStopped {
		t.Fatalf("telegram not disabled: %+v", got)
	}

	applied, err = srv.applyChannelSettings(ResolvedConfig{})
	if err != nil || !applied {
		t.Fatalf("no-op tick: applied=%v err=%v", applied, err)
	}
}
