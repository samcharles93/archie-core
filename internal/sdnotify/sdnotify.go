// Package sdnotify implements systemd's sd_notify(3) protocol: one READY=1 when
// a process starts serving, and a WATCHDOG=1 heartbeat while the loop it stands
// for is demonstrably still making progress.
//
// The protocol is three environment variables and one datagram per state, so
// this package speaks it directly instead of taking a module dependency: the
// whole client is net.DialUnix plus a Write. Nothing here is fatal -- a process
// with no NOTIFY_SOCKET (a plain terminal, Docker, a user unit without
// WatchdogSec) sends nothing and serves exactly as before, and a socket that
// cannot be written to is logged and ignored.
//
// It lives outside a composition package because a process that must announce
// itself cannot import another process's composition -- archied and
// archie-messaging are two processes that announce, and only one of them can
// own archied's boot
//
// What stays with the caller is everything that is the caller's: which marker
// counts as progress, how long its loop may go without a pass, and the point
// at which it considers itself serving.
//
// The heartbeat is deliberately not a bare ticker. A ticker fires whether or
// not the loop it stands for is alive, so it would keep systemd satisfied while
// the event loop was wedged -- the exact failure the watchdog exists to catch.
// The sampler instead reads the caller's own progress marker through Progress
// and withholds the heartbeat once that marker is older than the cadence
// Cadence reports. While passes keep beginning, beats flow at half of
// WatchdogSec; a loop blocked inside a pass stops being reported as alive.
//
// Consuming the states is the unit's business: READY=1 needs Type=notify, and
// the heartbeat needs WatchdogSec.
package sdnotify

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	// NotifySocketEnv names the socket a manager passes to a Type=notify unit.
	NotifySocketEnv = "NOTIFY_SOCKET"
	// WatchdogUsecEnv carries WatchdogSec in microseconds.
	WatchdogUsecEnv = "WATCHDOG_USEC"
	// WatchdogPIDEnv names the process whose beats the watchdog counts.
	WatchdogPIDEnv = "WATCHDOG_PID"

	// ReadyState and WatchdogState are the two sd_notify states a process
	// announces: it has started serving, and it is still making progress.
	ReadyState    = "READY=1"
	WatchdogState = "WATCHDOG=1"

	// watchdogHeartbeatDivisor is sd_notify(3)'s recommendation: beat at half
	// of WatchdogSec, so one missed beat is tolerated and two are not.
	watchdogHeartbeatDivisor = 2
)

// Notifier writes sd_notify states to the socket named in the environment. A
// notifier with no address -- the normal case away from systemd -- sends
// nothing and says nothing.
type Notifier struct {
	addr string
	log  *slog.Logger
}

// New resolves the notify socket. The address is read from the environment
// systemd sets for the process, so systemd replaces the previous unit's socket
// with the new one across a restart.
func New(getenv func(string) string, log *slog.Logger) Notifier {
	return Notifier{addr: getenv(NotifySocketEnv), log: log}
}

// Enabled reports whether a manager named a notify socket at all. It is the
// one fact a caller needs to decide whether a heartbeat is worth arming: with
// no socket every send is a no-op, and a sampler that cannot deliver a beat
// should not exist to withhold one.
func (n Notifier) Enabled() bool { return n.addr != "" }

// Send delivers one state line. A failure is logged, never returned: a
// notification is an observation about the process, and no observation is
// worth refusing to serve over.
func (n Notifier) Send(state string) {
	if n.addr == "" {
		return
	}
	if err := sendNotify(n.addr, state); err != nil {
		n.log.Warn("systemd notify failed", "state", state, "socket", n.addr, "err", err)
	}
}

// sendNotify writes one state datagram to a unixgram socket. sd_notify(3) is
// one datagram per state, so each send dials afresh: a manager restart recreates
// its socket, and a connection cached across one would fail every send after it.
// A leading '@' names the Linux abstract namespace, which user managers use;
// net.UnixAddr takes that syntax verbatim.
func sendNotify(addr, state string) error {
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("write %q: %w", state, err)
	}
	return nil
}

// Ready sends READY=1 to the socket named in the process environment. It is
// the one call a composing package needs to declare itself serving, and it
// belongs at the moment the process already says so: the daemon's run loop
// (next to its own health surface), and the serve steps of archie-gateway,
// archie-state-store and archie-messaging, which have readiness of their own
// and would otherwise hang a Type=notify unit until TimeoutStartSec while
// perfectly healthy.
func Ready(log *slog.Logger) {
	New(os.Getenv, log).Send(ReadyState)
}

// Heartbeat reads systemd's watchdog contract and returns the heartbeat
// cadence, which is half of WatchdogSec. ok is false when systemd asked for no
// watchdog, and also when it asked for one from a different process:
// WATCHDOG_PID names the process whose beats count, so a unit watching another
// process must not have this one beating on its behalf.
func Heartbeat(getenv func(string) string, pid int, log *slog.Logger) (time.Duration, bool) {
	raw := getenv(WatchdogUsecEnv)
	if raw == "" {
		return 0, false
	}
	if rawPID := getenv(WatchdogPIDEnv); rawPID != "" {
		wantPID, err := strconv.Atoi(rawPID)
		if err != nil || wantPID != pid {
			log.Warn("systemd watchdog disabled: WATCHDOG_PID is not this process",
				"watchdog_pid", rawPID, "pid", pid, "err", err)
			return 0, false
		}
	}
	watchdog, err := time.ParseDuration(raw + "us")
	if err != nil || watchdog <= 0 {
		log.Warn("systemd watchdog disabled: WATCHDOG_USEC unreadable", "value", raw, "err", err)
		return 0, false
	}
	return watchdog / watchdogHeartbeatDivisor, true
}

// Watchdog samples the caller's progress marker and emits the watchdog
// heartbeat only while that marker is fresh. Sampling is periodic; the beat is
// not, because it is licensed by the marker rather than by the clock.
type Watchdog struct {
	// Interval is the sampling cadence: half of systemd's WatchdogSec.
	Interval time.Duration
	// Cadence reports how long the loop may go between passes.
	Cadence func() time.Duration
	// Progress reports the start of the loop's most recent pass, or the zero
	// time when no pass has begun yet.
	Progress func() time.Time
	// Beat emits one WATCHDOG=1.
	Beat func()
	Log  *slog.Logger
}

// Run samples until ctx ends. It returns ctx.Err() on shutdown, which is the
// only way out: this sampler never gives up on its own, because a sampler that
// stopped would leave systemd watching a process nothing reports for.
func (w Watchdog) Run(ctx context.Context) error {
	armedAt := time.Now()
	stalled := false
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		last := w.Progress()
		if last.IsZero() {
			// No pass has begun at all. Measure staleness from arming, so a
			// loop that never starts polling is caught like one that stopped.
			last = armedAt
		}
		cadence := w.Cadence()
		switch age := time.Since(last); {
		case age > cadence:
			if !stalled {
				w.Log.Warn("systemd watchdog: run loop has not begun a poll pass within its interval; withholding the heartbeat until it does",
					"stale_for", age.String(), "loop_cadence", cadence.String())
				stalled = true
			}
		default:
			if stalled {
				w.Log.Info("systemd watchdog: run loop resumed; heartbeats resumed",
					"last_pass_age", age.String())
				stalled = false
			}
			w.Beat()
		}
	}
}
