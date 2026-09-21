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
	"github.com/samcharles93/archie-core/internal/store"
)

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
	t.Setenv(notifySocketEnv, addr)
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

// newNotifyTestBoot builds the composition the notify path reads: a boot whose
// config holder supplies the run loop's poll cadence, and a real daemon whose
// own poll loop stamps the progress marker the heartbeat sampler samples.
func newNotifyTestBoot(t *testing.T, cadence time.Duration) (*boot, *daemon.Daemon) {
	t.Helper()
	cfg := config.Config{PollInterval: config.Duration(cadence)}
	holder := config.NewHolder(cfg)
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("open task store: %v", err)
	}
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

// TestNotifyReachesAbstractSocket pins the '@' address form. systemd is not
// restricted to filesystem paths, and a client that stripped the prefix would
// connect to a file that does not exist instead of the abstract namespace.
func TestNotifyReachesAbstractSocket(t *testing.T) {
	listener := listenNotify(t, fmt.Sprintf("@archie-notify-test-%d", os.Getpid()))

	(&boot{log: slog.New(slog.DiscardHandler)}).announceReady()

	if states := listener.states(t, 300*time.Millisecond); !slices.Contains(states, readyState) {
		t.Errorf("states received = %v, want %q among them", states, readyState)
	}
}

// TestNotifierDisabledIsSilentAndUnusableSocketWarns pins the two ends of the
// delivery contract: no socket means nothing is attempted and nothing is said,
// while a socket that cannot be written to is reported once and then ignored.
// Neither may ever be fatal.
func TestNotifierDisabledIsSilentAndUnusableSocketWarns(t *testing.T) {
	rec := &notifyRecorder{}
	log := slog.New(rec)

	newNotifier(func(string) string { return "" }, log).send(readyState)
	if got := rec.all(); len(got) != 0 {
		t.Errorf("a notifier with no socket logged %d records, want none: %v", len(got), got)
	}

	missing := filepath.Join(t.TempDir(), "absent", "n.sock")
	newNotifier(func(string) string { return missing }, log).send(readyState)
	warns := rec.at(slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("sends to an unusable socket logged %d warnings, want exactly 1", len(warns))
	}
	if errs := rec.at(slog.LevelError); len(errs) != 0 {
		t.Errorf("sends to an unusable socket logged %d errors, want none", len(errs))
	}
}

// TestWatchdogHeartbeatInterval pins the interval convention: systemd passes
// WatchdogSec as WATCHDOG_USEC and sd_notify(3) heartbeats at half of it, and
// WATCHDOG_PID names the process whose beats count.
func TestWatchdogHeartbeatInterval(t *testing.T) {
	const self = 42
	tests := []struct {
		name   string
		usec   string
		pidEnv string
		want   time.Duration
		wantOK bool
	}{
		{name: "no watchdog requested", wantOK: false},
		{name: "half of a 30s watchdog", usec: "30000000", want: 15 * time.Second, wantOK: true},
		{name: "sub-second watchdog", usec: "20000", want: 10 * time.Millisecond, wantOK: true},
		{name: "unparseable", usec: "soon", wantOK: false},
		{name: "zero", usec: "0", wantOK: false},
		{name: "negative", usec: "-1", wantOK: false},
		{name: "watchdog pid is this process", usec: "20000", pidEnv: "42", want: 10 * time.Millisecond, wantOK: true},
		{name: "watchdog pid is another process", usec: "20000", pidEnv: "7", wantOK: false},
		{name: "watchdog pid is unreadable", usec: "20000", pidEnv: "seven", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string {
				switch key {
				case watchdogUsecEnv:
					return tt.usec
				case watchdogPIDEnv:
					return tt.pidEnv
				default:
					return ""
				}
			}
			got, ok := watchdogHeartbeat(getenv, self, slog.New(slog.DiscardHandler))
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("watchdogHeartbeat(usec=%q, pid=%q) = %v, %v; want %v, %v",
					tt.usec, tt.pidEnv, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestNotifyReadyAndWatchdogHeartbeat is the run-loop path end to end: a real
// NOTIFY_SOCKET, the daemon's own boot path announcing READY, and the run
// loop's own progress marker producing WATCHDOG heartbeats.
func TestNotifyReadyAndWatchdogHeartbeat(t *testing.T) {
	listener := newNotifyListener(t)
	t.Setenv(watchdogUsecEnv, "20000") // 20ms watchdog: heartbeat every 10ms

	b, d := newNotifyTestBoot(t, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var polling atomic.Bool
	polling.Store(true)
	go pollPasses(ctx, d, &polling, 5*time.Millisecond)

	b.announceReady()
	b.startWatchdog(ctx)

	states := listener.states(t, 300*time.Millisecond)
	if !slices.Contains(states, readyState) {
		t.Errorf("states received = %v, want %q among them", states, readyState)
	}
	if countState(states, watchdogState) == 0 {
		t.Errorf("states received = %v, want at least one %q among them", states, watchdogState)
	}
}

// TestWatchdogWithholdsHeartbeatWhenRunLoopStalls is the semantic that makes
// the heartbeat worth having: the beats stop once the loop stops making
// progress, so a wedged event loop trips systemd's watchdog instead of being
// kept alive by an independent ticker.
func TestWatchdogWithholdsHeartbeatWhenRunLoopStalls(t *testing.T) {
	listener := newNotifyListener(t)
	t.Setenv(watchdogUsecEnv, "20000") // 20ms watchdog: heartbeat every 10ms

	const cadence = 50 * time.Millisecond
	b, d := newNotifyTestBoot(t, cadence)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var polling atomic.Bool
	polling.Store(true)
	go pollPasses(ctx, d, &polling, 5*time.Millisecond)

	b.startWatchdog(ctx)
	if beats := countState(listener.states(t, 200*time.Millisecond), watchdogState); beats == 0 {
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
	t.Setenv(watchdogUsecEnv, "20000")
	// t.Setenv cannot unset a variable: register the restoration first, then
	// remove it for real so "unset" means unset.
	t.Setenv(notifySocketEnv, "")
	if err := os.Unsetenv(notifySocketEnv); err != nil {
		t.Fatalf("unset %s: %v", notifySocketEnv, err)
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
	t.Setenv(notifySocketEnv, missing)
	t.Setenv(watchdogUsecEnv, "20000")

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
