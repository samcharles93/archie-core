package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/sdnotify"
)

// The protocol-level properties of the notify client -- the '@' address form,
// the disabled and unusable socket contract, and the half-of-WatchdogSec
// heartbeat -- are pinned in internal/sdnotify, where the client now lives.
// These tests pin what is the daemon's: that its three serving entry points
// announce, that the sampler samples daemon.LastPollAt, and that the whole
// path stays silent and harmless without a NOTIFY_SOCKET.

// notifyRecorder captures every record at every level. The other recording
// handler in this package keeps warnings only; these tests need "this path
// logged nothing at all" as positive evidence that a disabled notifier stayed
// silent, so the level has to be part of what is captured.
type notifyRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *notifyRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *notifyRecorder) Handle(_ context.Context, r slog.Record) error {
	// The Record is the caller's to reuse once Handle returns, so keep a copy.
	rec := r.Clone()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec)
	return nil
}

func (h *notifyRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *notifyRecorder) WithGroup(string) slog.Handler      { return h }

func (h *notifyRecorder) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

// at returns the captured records at one level.
func (h *notifyRecorder) at(level slog.Level) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Level == level {
			out = append(out, r)
		}
	}
	return out
}

// notifyListener is a stand-in for systemd's NOTIFY_SOCKET: a unixgram socket
// that collects the state lines the daemon sends. It is bound to the real
// NOTIFY_SOCKET for the duration of the test.
type notifyListener struct {
	conn *net.UnixConn
}

func newNotifyListener(t *testing.T) *notifyListener {
	t.Helper()
	// A unix socket path is capped near 108 bytes; the short file name keeps the
	// path inside t.TempDir() under that for any test name.
	return listenNotify(t, filepath.Join(t.TempDir(), "n.sock"))
}

// listenNotify binds a notify socket at addr and points NOTIFY_SOCKET at it.
func listenNotify(t *testing.T, addr string) *notifyListener {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen for notify datagrams at %q: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	t.Setenv(sdnotify.NotifySocketEnv, addr)
	return &notifyListener{conn: conn}
}

// states drains every state line received within window.
func (l *notifyListener) states(t *testing.T, window time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(window)
	var got []string
	buf := make([]byte, 1024)
	for {
		if err := l.conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set notify read deadline: %v", err)
		}
		n, _, err := l.conn.ReadFromUnix(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return got
			}
			t.Fatalf("read notify datagram: %v", err)
		}
		got = append(got, string(buf[:n]))
	}
}

// countState counts the datagrams carrying want among the received states.
func countState(states []string, want string) int {
	n := 0
	for _, state := range states {
		if state == want {
			n++
		}
	}
	return n
}

// waitForState reads datagrams until one carries want or the window closes.
// The states drain above collects a window's worth; a boot-path test waiting
// for one state returns as soon as it arrives.
func (l *notifyListener) waitForState(t *testing.T, window time.Duration, want string) bool {
	t.Helper()
	deadline := time.Now().Add(window)
	buf := make([]byte, 1024)
	for time.Now().Before(deadline) {
		if err := l.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			t.Fatalf("set notify read deadline: %v", err)
		}
		n, _, err := l.conn.ReadFromUnix(buf)
		if err != nil {
			continue
		}
		if string(buf[:n]) == want {
			return true
		}
	}
	return false
}

// newNotifyTestBoot builds the composition the notify path reads: a boot whose
// config holder supplies the run loop's poll cadence, and a real daemon whose
// own poll loop stamps the progress marker the heartbeat sampler samples.
func newNotifyTestBoot(t *testing.T, cadence time.Duration) (*boot, *daemon.Daemon) {
	t.Helper()
	cfg := config.Config{PollInterval: config.Duration(cadence)}
	holder := config.NewHolder(cfg)
	st := pgstore.Open(t)
	log := slog.New(slog.DiscardHandler)
	d := &daemon.Daemon{Cfg: holder, Store: st, Log: log}
	return &boot{cfg: cfg, cfgHolder: holder, d: d, log: log}, d
}

// pollPasses drives the daemon's poll passes the way Run does, one Cycle per
// tick, while on is set. The progress marker is stamped by the daemon inside
// Cycle, never by the test: what the sampler samples is the loop's own
// evidence, not a value the test wrote.
func pollPasses(ctx context.Context, d *daemon.Daemon, on *atomic.Bool, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if on.Load() {
				d.Cycle(ctx)
			}
		}
	}
}

// TestNotifyReadyAndWatchdogHeartbeat is the run-loop path end to end: a real
// NOTIFY_SOCKET, the daemon's own boot path announcing READY, and the run
// loop's own progress marker producing WATCHDOG heartbeats.
func TestNotifyReadyAndWatchdogHeartbeat(t *testing.T) {
	listener := newNotifyListener(t)
	t.Setenv(sdnotify.WatchdogUsecEnv, "20000") // 20ms watchdog: heartbeat every 10ms

	b, d := newNotifyTestBoot(t, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var polling atomic.Bool
	polling.Store(true)
	go pollPasses(ctx, d, &polling, 5*time.Millisecond)

	b.announceReady()
	b.startWatchdog(ctx)

	states := listener.states(t, 300*time.Millisecond)
	if !slices.Contains(states, sdnotify.ReadyState) {
		t.Errorf("states received = %v, want %q among them", states, sdnotify.ReadyState)
	}
	if countState(states, sdnotify.WatchdogState) == 0 {
		t.Errorf("states received = %v, want at least one %q among them", states, sdnotify.WatchdogState)
	}
}

// TestWatchdogWithholdsHeartbeatWhenRunLoopStalls is the semantic that makes
// the heartbeat worth having: the beats stop once the loop stops making
// progress, so a wedged event loop trips systemd's watchdog instead of being
// kept alive by an independent ticker.
func TestWatchdogWithholdsHeartbeatWhenRunLoopStalls(t *testing.T) {
	listener := newNotifyListener(t)
	t.Setenv(sdnotify.WatchdogUsecEnv, "20000") // 20ms watchdog: heartbeat every 10ms

	const cadence = 50 * time.Millisecond
	b, d := newNotifyTestBoot(t, cadence)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var polling atomic.Bool
	polling.Store(true)
	go pollPasses(ctx, d, &polling, 5*time.Millisecond)

	b.startWatchdog(ctx)
	if beats := countState(listener.states(t, 200*time.Millisecond), sdnotify.WatchdogState); beats == 0 {
		t.Fatal("no WATCHDOG=1 within 200ms while the run loop was polling")
	}

	// From here the loop is inside a pass that never returns: nothing stamps
	// the progress marker again.
	polling.Store(false)

	// Discard the beats the last stamp still licenses. The marker counts as
	// stale only once it is older than the loop's own cadence (50ms), and
	// samples are 10ms apart, so 150ms covers every remaining beat with margin.
	listener.states(t, 150*time.Millisecond)

	if late := listener.states(t, 300*time.Millisecond); len(late) != 0 {
		t.Errorf("received %v after the run loop stalled, want no heartbeats", late)
	}
}

// TestNotifyUnsetIsNoOp pins the deployment contract: with no NOTIFY_SOCKET the
// process behaves exactly as it did before sd_notify existed. Docker, a plain
// terminal, and a systemd unit with no WatchdogSec all take this path, and none
// of them may see a datagram, a warning, or a failed start.
func TestNotifyUnsetIsNoOp(t *testing.T) {
	t.Setenv(sdnotify.WatchdogUsecEnv, "20000")
	// t.Setenv cannot unset a variable: register the restoration first, then
	// remove it for real so "unset" means unset.
	t.Setenv(sdnotify.NotifySocketEnv, "")
	if err := os.Unsetenv(sdnotify.NotifySocketEnv); err != nil {
		t.Fatalf("unset %s: %v", sdnotify.NotifySocketEnv, err)
	}

	rec := &notifyRecorder{}
	b, _ := newNotifyTestBoot(t, 50*time.Millisecond)
	b.log = slog.New(rec)

	b.announceReady()
	b.startWatchdog(t.Context())

	if got := rec.all(); len(got) != 0 {
		t.Errorf("with NOTIFY_SOCKET unset the notify path logged %d records, want none: %v", len(got), got)
	}
}

// TestNotifySocketFailureIsNotFatal pins the other half of the deployment
// contract: a NOTIFY_SOCKET that cannot be written to is logged and ignored.
// A daemon that refused to serve because systemd's socket was unavailable
// would be a regression on every deployment that has no systemd at all.
func TestNotifySocketFailureIsNotFatal(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent", "n.sock")
	t.Setenv(sdnotify.NotifySocketEnv, missing)
	t.Setenv(sdnotify.WatchdogUsecEnv, "20000")

	rec := &notifyRecorder{}
	b, d := newNotifyTestBoot(t, 50*time.Millisecond)
	b.log = slog.New(rec)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var polling atomic.Bool
	polling.Store(true)
	go pollPasses(ctx, d, &polling, 5*time.Millisecond)

	b.announceReady()
	b.startWatchdog(ctx)

	if warns := rec.at(slog.LevelWarn); len(warns) != 1 {
		t.Errorf("announceReady against an unusable socket logged %d warnings, want exactly 1", len(warns))
	}

	// The sampler keeps trying rather than dying on the first failure, so more
	// warnings arrive while the loop progresses.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.at(slog.LevelWarn)) > 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if warns := rec.at(slog.LevelWarn); len(warns) < 2 {
		t.Errorf("the watchdog logged %d warnings, want the heartbeat still being attempted", len(warns))
	}
	if errs := rec.at(slog.LevelError); len(errs) != 0 {
		t.Errorf("an unusable notify socket logged %d errors, want none: %v", len(errs), errs)
	}
}

// TestStateStoreBootAnnouncesReady: archie-state-store run as a Type=notify
// unit must send READY=1 once its boot is over and it is serving the contract,
// or systemd kills a healthy process once TimeoutStartSec expires
// (archie-core-1174). The real RunStateStore composition drives it, over a
// real State Store database, the way the provider-seeding boot test does.
func TestStateStoreBootAnnouncesReady(t *testing.T) {
	listener := newNotifyListener(t)
	url := pgtest.URL(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	body := fmt.Sprintf("bot_user = 'archie'\ndatabase_url = %q\n", url)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- RunStateStore(ctx, StateStoreOptions{Config: path, Listen: "127.0.0.1:0"}) }()

	if !listener.waitForState(t, 15*time.Second, sdnotify.ReadyState) {
		select {
		case bootErr := <-errCh:
			t.Fatalf("RunStateStore exited before announcing ready: %v", bootErr)
		default:
			t.Fatal("no READY=1 datagram within 15s of boot")
		}
	}

	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("RunStateStore shutdown: %v", err)
	}
}
