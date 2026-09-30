package sdnotify

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
	"testing"
	"time"
)

// notifyRecorder captures every record at every level. A warnings-only
// recorder would not do: these tests need "this path logged nothing at all" as
// positive evidence that a disabled notifier stayed silent, so the level has
// to be part of what is captured.
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
// that collects the state lines a process sends. It is bound to the real
// NOTIFY_SOCKET for the duration of the test.
type notifyListener struct {
	conn *net.UnixConn
}

// listenNotify binds a notify socket at addr and points NOTIFY_SOCKET at it.
func listenNotify(t *testing.T, addr string) *notifyListener {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen for notify datagrams at %q: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	t.Setenv(NotifySocketEnv, addr)
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

// TestNotifyReachesAbstractSocket pins the '@' address form. systemd is not
// restricted to filesystem paths, and a client that stripped the prefix would
// connect to a file that does not exist instead of the abstract namespace.
func TestNotifyReachesAbstractSocket(t *testing.T) {
	listener := listenNotify(t, fmt.Sprintf("@archie-notify-test-%d", os.Getpid()))

	Ready(slog.New(slog.DiscardHandler))

	if states := listener.states(t, 300*time.Millisecond); !slices.Contains(states, ReadyState) {
		t.Errorf("states received = %v, want %q among them", states, ReadyState)
	}
}

// TestNotifierDisabledIsSilentAndUnusableSocketWarns pins the two ends of the
// delivery contract: no socket means nothing is attempted and nothing is said,
// while a socket that cannot be written to is reported once and then ignored.
// Neither may ever be fatal.
func TestNotifierDisabledIsSilentAndUnusableSocketWarns(t *testing.T) {
	rec := &notifyRecorder{}
	log := slog.New(rec)

	New(func(string) string { return "" }, log).Send(ReadyState)
	if got := rec.all(); len(got) != 0 {
		t.Errorf("a notifier with no socket logged %d records, want none: %v", len(got), got)
	}

	missing := filepath.Join(t.TempDir(), "absent", "n.sock")
	New(func(string) string { return missing }, log).Send(ReadyState)
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
				case WatchdogUsecEnv:
					return tt.usec
				case WatchdogPIDEnv:
					return tt.pidEnv
				default:
					return ""
				}
			}
			got, ok := Heartbeat(getenv, self, slog.New(slog.DiscardHandler))
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("Heartbeat(usec=%q, pid=%q) = %v, %v; want %v, %v",
					tt.usec, tt.pidEnv, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
