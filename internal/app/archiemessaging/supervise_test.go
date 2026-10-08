package archiemessaging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// fakeChannel is a channel whose runs a test scripts: it counts starts and
// either fails immediately or blocks until its run context ends.
type fakeChannel struct {
	mu     sync.Mutex
	starts int
	runErr error
}

func (f *fakeChannel) Name() string { return "fake" }

func (f *fakeChannel) Start(ctx context.Context, _ messaging.ChatContract, lifecycle channels.Lifecycle) error {
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()
	if lifecycle.Starting != nil {
		lifecycle.Starting()
	}
	if f.runErr != nil {
		return f.runErr
	}
	<-ctx.Done()
	return nil
}

func (f *fakeChannel) Stop(context.Context) error { return nil }

func (f *fakeChannel) ConfigSchema() json.RawMessage { return nil }

func (f *fakeChannel) ValidateConfig(map[string]any) error { return nil }

func (f *fakeChannel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// startService composes and starts a service over one scripted channel, with a
// retry policy the test drives without waiting out real backoff.
func startService(t *testing.T, ch *fakeChannel, retry channelRetryPolicy) *Service {
	t.Helper()
	inst := &channelInstance{
		name:    "fake",
		channel: ch,
		rebuild: func(ResolvedConfig) (channels.Channel, error) { return ch, nil },
	}
	srv, err := compose(context.Background(), deps{Log: slog.Default(), ExtensionChannels: []*channelInstance{inst}})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	srv.retry = retry
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
	return srv
}

func waitStarts(t *testing.T, ch *fakeChannel, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ch.count() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("channel started %d times, want at least %d", ch.count(), want)
}

// TestAwaitChannelActiveStates pins what counts as up: running and degraded
// satisfy an enable, only stopped satisfies a disable, and a failed enable
// fails fast instead of reporting success the next tick would never retry.
func TestAwaitChannelActiveStates(t *testing.T) {
	mark := map[string]func(*status.Manager){
		"running":  func(m *status.Manager) { m.MarkRunning("fake") },
		"degraded": func(m *status.Manager) { m.MarkDegraded("fake", "slow") },
		"failed":   func(m *status.Manager) { m.MarkFailed("fake", "boom") },
		"starting": func(m *status.Manager) { m.MarkStarting("fake") },
	}
	// Refusals that wait out the 5s timeout (disable while failed or
	// running) are pre-existing behavior, unchanged here, and too slow to
	// pin; every case below answers at once.
	tests := []struct {
		name    string
		state   string
		active  bool
		wantErr bool
	}{
		{name: "enable accepts running", state: "running", active: true},
		{name: "enable accepts degraded", state: "degraded", active: true},
		{name: "enable refuses failed", state: "failed", active: true, wantErr: true},
		{name: "disable accepts stopped", state: "", active: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := status.NewManager([]status.Descriptor{{ID: "fake"}})
			if tt.state == "" {
				manager.MarkStopped("fake", "")
			} else {
				mark[tt.state](manager)
			}
			srv := &Service{status: manager}
			// Every case above answers at once: success and failed-enable
			// refusal never reach the timeout wait.
			done := make(chan error, 1)
			go func() { done <- srv.awaitChannelActive("fake", tt.active) }()
			select {
			case err := <-done:
				if (err != nil) != tt.wantErr {
					t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("await did not answer")
			}
		})
	}
}

// TestChannelSupervisorRetriesAndRestarts pins that a channel that fails on its
// own is retried with bounded backoff and, once the budget is spent, parked so
// an explicit restart brings it back rather than being refused.
func TestChannelSupervisorRetriesAndRestarts(t *testing.T) {
	ch := &fakeChannel{runErr: errors.New("transport down")}
	srv := startService(t, ch, channelRetryPolicy{attempts: 3, backoff: time.Millisecond, maxBackoff: time.Millisecond})

	// The supervisor retries the failed run instead of abandoning the channel.
	waitStarts(t, ch, 3)

	if err := srv.restartChannel("fake", ResolvedConfig{}); err != nil {
		t.Fatalf("restart after the retry budget was spent: %v", err)
	}
	waitStarts(t, ch, 4)
}

// TestChannelSupervisorRestartReplacesRunningChannel pins the in-flight path: a
// restart of a channel whose run is live cancels the run and starts the
// replacement, with no backoff.
func TestChannelSupervisorRestartReplacesRunningChannel(t *testing.T) {
	ch := &fakeChannel{} // blocks until its run context ends
	srv := startService(t, ch, channelRetryPolicy{attempts: 3, backoff: time.Hour, maxBackoff: time.Hour})

	waitStarts(t, ch, 1)
	if err := srv.restartChannel("fake", ResolvedConfig{}); err != nil {
		t.Fatalf("restart running channel: %v", err)
	}
	waitStarts(t, ch, 2)
}

// TestChannelRestartRefusedWithoutSupervisor pins that a channel with no
// supervisor -- never started, or already shut down -- is refused rather than
// signalling a goroutine that is gone.
func TestChannelRestartRefusedWithoutSupervisor(t *testing.T) {
	ch := &fakeChannel{}
	inst := &channelInstance{
		name:    "fake",
		channel: ch,
		rebuild: func(ResolvedConfig) (channels.Channel, error) { return ch, nil },
	}
	srv, err := compose(context.Background(), deps{Log: slog.Default(), ExtensionChannels: []*channelInstance{inst}})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if err := srv.restartChannel("fake", ResolvedConfig{}); err == nil {
		t.Fatal("restart before the service started should be refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Start(ctx)
	}()
	waitStarts(t, ch, 1)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	if err := srv.restartChannel("fake", ResolvedConfig{}); err == nil {
		t.Fatal("restart after shutdown should be refused")
	}
}
