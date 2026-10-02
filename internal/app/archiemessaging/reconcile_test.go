package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// channelByID returns the live instance the service currently serves for id.
func channelByID(t *testing.T, srv *Service, id string) *channelInstance {
	t.Helper()
	for _, instance := range srv.channels {
		if instance.name == id {
			return instance
		}
	}
	t.Fatalf("no channel %q composed", id)
	return nil
}

// channelAddrs reserves two loopback ports, one per channel.
func channelAddrs(t *testing.T) (emailAddr, webhookAddr string) {
	t.Helper()
	return fmt.Sprintf("127.0.0.1:%d", freePort(t)), fmt.Sprintf("127.0.0.1:%d", freePort(t))
}

// writeChannelConfig writes a file document with email and webhook enabled. A
// file rather than a hand-built ResolvedConfig, because the reconcile path
// re-resolves the file exactly as production does; a hand-built config would
// prove nothing about that half.
func writeChannelConfig(t *testing.T, emailAddr, webhookAddr, route string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := fmt.Sprintf("bot_user = \"testbot\"\n\n[chat]\nwebhook_addr = %q\n\n[chat.email]\nlisten_addr = %q\n\n[chat.webhook]\npath = %q\n",
		webhookAddr, emailAddr, route)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func resolveChannels(t *testing.T, emailAddr, webhookAddr, route string) ResolvedConfig {
	t.Helper()
	cfg, err := Resolve(
		Options{Config: writeChannelConfig(t, emailAddr, webhookAddr, route), ShutdownTimeout: 2 * time.Second},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return cfg
}

// startComposed starts a composed service and waits for its channels to run.
func startComposed(t *testing.T, d deps) *Service {
	t.Helper()
	srv, err := compose(t.Context(), d)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan error, 1)
	go func() { started <- srv.Start(ctx) }()
	t.Cleanup(func() {
		srv.Stop()
		cancel()
		select {
		case err := <-started:
			if err != nil {
				t.Errorf("srv.Start returned %v, want a clean stop", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("srv.Start did not return after Stop")
		}
	})
	waitForState(t, srv, "email", status.StateRunning)
	waitForState(t, srv, "webhook", status.StateRunning)
	return srv
}

// startChannels composes and starts email and webhook. Those two are the
// channels a test can run without an external service, and a restart of one is
// observable as the channel pointer changing.
func startChannels(t *testing.T) *Service {
	t.Helper()
	emailAddr, webhookAddr := channelAddrs(t)
	return startComposed(t, deps{
		Config: resolveChannels(t, emailAddr, webhookAddr, "/a"),
		Log:    slog.New(slog.DiscardHandler),
		Chat:   &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
	})
}

// TestApplyChannelSettingsRestartsOnlyTheChangedChannel is the bead's core
// property: a change to one channel restarts that channel and leaves the others
// serving. Before the per-channel supervisor this could only be a process
// restart, which restarted every channel (archie-core-zfb0.4).
func TestApplyChannelSettingsRestartsOnlyTheChangedChannel(t *testing.T) {
	srv := startChannels(t)
	webhookBefore := channelByID(t, srv, "webhook").current()
	emailBefore := channelByID(t, srv, "email").current()

	next := srv.currentConfig()
	next.Webhook = config.WebhookRoute{Path: "/b"}
	if _, err := srv.applyChannelSettings(next); err != nil {
		t.Fatalf("applyChannelSettings: %v", err)
	}

	if got := channelByID(t, srv, "webhook").current(); got == webhookBefore {
		t.Error("webhook did not restart after its route changed")
	}
	if got := channelByID(t, srv, "email").current(); got != emailBefore {
		t.Error("email restarted when only the webhook route changed")
	}
}

// TestApplyChannelSettingsRefusesAListenAddressChange is the path where the
// change must NOT apply: a bound listen address is a process binding, and
// rebinding it in place is exactly the restart the epic keeps. The running
// channel is left alone and the change is reported instead.
func TestApplyChannelSettingsRefusesAListenAddressChange(t *testing.T) {
	srv := startChannels(t)
	before := channelByID(t, srv, "webhook").current()

	next := srv.currentConfig()
	next.WebhookAddr = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	_, err := srv.applyChannelSettings(next)
	if err == nil {
		t.Fatal("applyChannelSettings error = nil, want the listen-address change reported")
	}
	if !strings.Contains(err.Error(), "webhook_addr") {
		t.Errorf("error = %v, want it to name chat.webhook_addr", err)
	}
	if got := channelByID(t, srv, "webhook").current(); got != before {
		t.Error("webhook restarted for a listen-address change; a bound address must not move in place")
	}
}

// TestApplyChannelSettingsRestartsNothingWhenNothingChanged: the other half of
// "only the channels whose settings changed" -- a tick that carries no change
// must not restart a healthy channel.
func TestApplyChannelSettingsRestartsNothingWhenNothingChanged(t *testing.T) {
	srv := startChannels(t)
	webhookBefore := channelByID(t, srv, "webhook").current()
	emailBefore := channelByID(t, srv, "email").current()

	if _, err := srv.applyChannelSettings(srv.currentConfig()); err != nil {
		t.Fatalf("applyChannelSettings: %v", err)
	}
	if channelByID(t, srv, "webhook").current() != webhookBefore {
		t.Error("webhook restarted with no settings change")
	}
	if channelByID(t, srv, "email").current() != emailBefore {
		t.Error("email restarted with no settings change")
	}
}

// fakeChannelSettings is the stored channel-settings resource a test owns. It
// returns the document a real store would: the file's values with the stored
// route layered over them, so the reconcile's layering is exercised rather than
// bypassed.
type fakeChannelSettings struct {
	mu       sync.Mutex
	settings config.ChatConfig
	version  int64
}

func (f *fakeChannelSettings) RuntimeChatConfig(_ context.Context, base config.ChatConfig) (config.ChatConfig, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := base
	out.Webhook = f.settings.Webhook
	return out, f.version, nil
}

func (f *fakeChannelSettings) set(version int64, route config.WebhookRoute) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version, f.settings.Webhook = version, route
}

// recordingApplyStore records the apply-status writes the service makes.
type recordingApplyStore struct {
	mu     sync.Mutex
	writes []storecontract.ApplyStatus
}

func (s *recordingApplyStore) PutApplyStatus(_ context.Context, st storecontract.ApplyStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, st)
	return nil
}

func (s *recordingApplyStore) ListApplyStatus(context.Context) ([]storecontract.ApplyStatus, error) {
	return nil, nil
}

func (s *recordingApplyStore) appliedVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var version int64
	for _, st := range s.writes {
		version = max(version, st.AppliedVersion)
	}
	return version
}

// TestReconcileAppliesANewStoredVersion drives the whole live path: the service
// reads the stored channel settings on its interval, sees a newer version, and
// restarts the channel it changed. The applied version reaches apply status, so
// the dashboard stops showing the change as pending.
func TestReconcileAppliesANewStoredVersion(t *testing.T) {
	emailAddr, webhookAddr := channelAddrs(t)
	source := &fakeChannelSettings{}
	source.set(1, config.WebhookRoute{Path: "/a"})
	store := &recordingApplyStore{}

	srv := startComposed(t, deps{
		Config:            resolveChannels(t, emailAddr, webhookAddr, "/a"),
		Log:               slog.New(slog.DiscardHandler),
		Chat:              &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
		SettingsSource:    source,
		ApplyReporter:     applystatus.New(applystatus.Messaging, store, slog.New(slog.DiscardHandler)),
		AppliedVersion:    1,
		ReconcileInterval: 5 * time.Millisecond,
	})

	before := channelByID(t, srv, "webhook").current()
	source.set(2, config.WebhookRoute{Path: "/b"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if channelByID(t, srv, "webhook").current() != before && store.appliedVersion() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("after the stored version moved to 2: channel replaced=%v, applied version=%d; want both",
		channelByID(t, srv, "webhook").current() != before, store.appliedVersion())
}
