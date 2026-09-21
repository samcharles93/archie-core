package archiemessaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// waitForState polls the service's own report until the channel reaches want, so
// the assertion races the channel's goroutine rather than the clock.
func waitForState(t *testing.T, srv *Service, id string, want status.State) status.Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last status.Status
	for time.Now().Before(deadline) {
		for _, candidate := range srv.ChannelStatus() {
			if candidate.ID != id {
				continue
			}
			last = candidate
			if candidate.State == want {
				return candidate
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("channel %s never reached %s; last report %+v", id, want, last)
	return status.Status{}
}

// TestServiceRecordsChannelLifecycle pins the fact archie-core-8cda.6.8 is about:
// a channel's own report reaches a manager. Before this the service handed every
// channel an empty Lifecycle, so starting/running/stopped were recorded nowhere
// and internal/channels/status.Manager had no production writer at all.
func TestServiceRecordsChannelLifecycle(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	srv, err := compose(t.Context(), deps{
		Config: ResolvedConfig{
			Options:     Options{ShutdownTimeout: 2 * time.Second},
			WebhookAddr: addr,
			Webhook:     config.WebhookRoute{Path: "/smoke"},
		},
		Log:  slog.Default(),
		Chat: &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	// Composed but not started: the only honest thing to report is configured.
	composed := srv.ChannelStatus()
	if len(composed) != 1 || composed[0].State != status.StateConfigured {
		t.Fatalf("ChannelStatus before Start = %+v, want one configured channel", composed)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- srv.Start(ctx) }()

	running := waitForState(t, srv, "webhook", status.StateRunning)

	srv.Stop()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("srv.Start returned %v, want a clean stop", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("srv.Start did not return after Stop")
	}
	stopped := waitForState(t, srv, "webhook", status.StateStopped)
	if stopped.State != status.StateStopped {
		t.Fatalf("state after Stop = %s, want stopped", stopped.State)
	}

	// The descriptor is the operator-facing half, and it must not carry
	// configuration: a token in here would make the manager a second source for a
	// credential.
	if !running.Configured || running.Name != "webhook" {
		t.Errorf("status = %+v, want a configured, named webhook", running)
	}
	if running.ReloadSupported {
		t.Error("webhook declares reload support, but no reload seam backs it")
	}
}

// TestServiceRecordsAChannelThatCannotStart is the half a status surface exists
// for: a channel that died must be visible on the operator's surface, not only in
// the log, because "nothing reported" and "nothing running" look identical on a
// dashboard.
func TestServiceRecordsAChannelThatCannotStart(t *testing.T) {
	// Hold the port, so the channel's own listen fails deterministically.
	var lc net.ListenConfig
	occupied, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	srv, err := compose(t.Context(), deps{
		Config: ResolvedConfig{
			Options:     Options{ShutdownTimeout: 2 * time.Second},
			WebhookAddr: occupied.Addr().String(),
			Webhook:     config.WebhookRoute{Path: "/smoke"},
		},
		Log:  slog.Default(),
		Chat: &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- srv.Start(ctx) }()
	t.Cleanup(srv.Stop)

	failed := waitForState(t, srv, "webhook", status.StateFailed)
	if failed.Detail == "" {
		t.Error("a failed channel carries no detail; the operator cannot tell why it is down")
	}
}

// TestChannelDescriptorsDeclareCapabilities pins the declarations the dashboard
// acts on. Reload is the one with a real consequence: a channel declaring it
// invites a button that must do something.
func TestChannelDescriptorsDeclareCapabilities(t *testing.T) {
	// Telegram is a real gateway with a reload seam: the capability now derives
	// from the channel and its seam, not from its name, so a fakeChannel named
	// "telegram" correctly declares nothing.
	withReload := telegram.New("token-123", []int64{1}, slog.New(slog.DiscardHandler))
	withReload.Reload = func(*telegram.Gateway) error { return nil }
	descriptors := channelDescriptors([]channelInstance{
		{name: "telegram", channel: withReload},
		{name: "email", channel: fakeChannel{}},
		{name: "webhook", channel: fakeChannel{}},
	})
	if len(descriptors) != 3 {
		t.Fatalf("descriptors = %+v, want one per composed channel", descriptors)
	}
	byID := map[string]status.Descriptor{}
	for _, descriptor := range descriptors {
		byID[descriptor.ID] = descriptor
	}
	if !byID["telegram"].ReloadSupported {
		t.Error("telegram does not declare reload support, but it is the channel with a reload seam")
	}
	for _, id := range []string{"email", "webhook"} {
		if byID[id].ReloadSupported {
			t.Errorf("%s declares reload support with nothing backing it", id)
		}
	}
	for id, descriptor := range byID {
		if !descriptor.Configured {
			t.Errorf("%s is composed but not declared configured", id)
		}
	}
}

// fakeChannel satisfies channels.Channel for descriptor tests, which read identity
// and capabilities only.
type fakeChannel struct{}

func (fakeChannel) Name() string                        { return "fake" }
func (fakeChannel) Stop(context.Context) error          { return nil }
func (fakeChannel) ConfigSchema() json.RawMessage       { return json.RawMessage(`{}`) }
func (fakeChannel) ValidateConfig(map[string]any) error { return nil }
func (fakeChannel) Start(context.Context, messaging.ChatContract, channels.Lifecycle) error {
	return nil
}
