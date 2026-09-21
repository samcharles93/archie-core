// sd_notify.go implements systemd's sd_notify(3) protocol for archied: one
// READY=1 when the run loop starts serving, and a WATCHDOG=1 heartbeat while
// the run loop is demonstrably still making progress.
//
// The protocol is three environment variables and one datagram per state, so
// this file speaks it directly instead of taking a module dependency: the whole
// client is net.DialUnix plus a Write. Nothing here is fatal -- a process with
// no NOTIFY_SOCKET (a plain terminal, Docker, a user unit without WatchdogSec)
// sends nothing and serves exactly as before, and a socket that cannot be
// written to is logged and ignored.
//
// The heartbeat is deliberately not a bare ticker. A ticker fires whether or
// not the loop it stands for is alive, so it would keep systemd satisfied while
// the event loop was wedged -- the exact failure the watchdog exists to catch.
// The sampler instead reads the daemon's own progress marker
// (daemon.LastPollAt, stamped by the poll loop at the start of every pass) and
// withholds the heartbeat once that marker is older than the loop's own poll
// cadence. While passes keep beginning, beats flow at half of WatchdogSec; a
// loop blocked inside a pass stops being reported as alive.
//
// Consuming the states is the unit's business: READY=1 needs Type=notify, and
// the heartbeat needs WatchdogSec.
package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	// notifySocketEnv names the socket a manager passes to a Type=notify unit.
	notifySocketEnv = "NOTIFY_SOCKET"
	// watchdogUsecEnv carries WatchdogSec in microseconds.
	watchdogUsecEnv = "WATCHDOG_USEC"
	// watchdogPIDEnv names the process whose beats the watchdog counts.
	watchdogPIDEnv = "WATCHDOG_PID"

	// readyState and watchdogState are the two sd_notify states archied sends.
	readyState    = "READY=1"
	watchdogState = "WATCHDOG=1"

	// watchdogHeartbeatDivisor is sd_notify(3)'s recommendation: beat at half
	// of WatchdogSec, so one missed beat is tolerated and two are not.
	watchdogHeartbeatDivisor = 2
)

// notifier writes sd_notify states to the socket named in the environment. A
// notifier with no address -- the normal case away from systemd -- sends
// nothing and says nothing.
type notifier struct {
	addr string
	log  *slog.Logger
}

// newNotifier resolves the notify socket. The address is read from the
// environment systemd sets for the process, so systemd replaces the previous
// unit's socket with the new one across a restart.
func newNotifier(getenv func(string) string, log *slog.Logger) notifier {
	return notifier{addr: getenv(notifySocketEnv), log: log}
}

// send delivers one state line. A failure is logged, never returned: a
// notification is an observation about the daemon, and no observation is worth
// refusing to serve over.
func (n notifier) send(state string) {
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

// announceReady tells systemd the daemon has finished starting. It is called at
// the moment the process declares itself serving (runLoop, next to
// healthSurface.markServing), which is the same fact READY=1 asserts: boot is
// over and the run loop is about to take over.
func (b *boot) announceReady() {
	newNotifier(os.Getenv, b.log).send(readyState)
}

// startWatchdog arms the heartbeat sampler for the daemon's run loop. It does
// nothing unless a manager named a notify socket and asked for a watchdog
// (WATCHDOG_USEC), so every deployment without systemd is unaffected.
func (b *boot) startWatchdog(ctx context.Context) {
	n := newNotifier(os.Getenv, b.log)
	if n.addr == "" {
		return
	}
	interval, ok := watchdogHeartbeat(os.Getenv, os.Getpid(), b.log)
	if !ok {
		return
	}

	sampler := loopWatchdog{
		interval: interval,
		cadence:  b.loopCadence,
		progress: b.d.LastPollAt,
		beat:     func() { n.send(watchdogState) },
		log:      b.log,
	}
	b.log.Info("systemd watchdog armed",
		"heartbeat", interval.String(),
		"loop_cadence", b.loopCadence().String())

	go func() {
		if err := sampler.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			b.log.Error("systemd watchdog stopped", "err", err)
		}
	}()
}

// loopCadence is the longest interval at which the run loop may go without
// beginning a fresh poll pass, and therefore how stale the progress marker may
// be before the loop counts as stalled.
//
// It is the root poll interval, or the slowest configured identity in
// multi-identity mode: each identity polls its own repos, identity poll
// intervals are frozen at boot, and the marker is stamped by whichever identity
// polled most recently -- so the worst case between stamps is the slowest
// identity, not the fastest. The root value is re-read per sample, so a
// reloaded poll_interval is honoured.
func (b *boot) loopCadence() time.Duration {
	cadence := b.cfgHolder.Get().PollInterval.Std()
	for _, id := range b.d.Identities {
		if override := id.Cfg.PollInterval.Std(); override > cadence {
			cadence = override
		}
	}
	return cadence
}

// watchdogHeartbeat reads systemd's watchdog contract and returns the heartbeat
// cadence, which is half of WatchdogSec. ok is false when systemd asked for no
// watchdog, and also when it asked for one from a different process:
// WATCHDOG_PID names the process whose beats count, so a unit watching another
// process must not have this one beating on its behalf.
func watchdogHeartbeat(getenv func(string) string, pid int, log *slog.Logger) (time.Duration, bool) {
	raw := getenv(watchdogUsecEnv)
	if raw == "" {
		return 0, false
	}
	if rawPID := getenv(watchdogPIDEnv); rawPID != "" {
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

// loopWatchdog samples the run loop's progress marker and emits the watchdog
// heartbeat only while that marker is fresh. Sampling is periodic; the beat is
// not, because it is licensed by the marker rather than by the clock.
type loopWatchdog struct {
	// interval is the sampling cadence: half of systemd's WatchdogSec.
	interval time.Duration
	// cadence reports how long the loop may go between poll passes.
	cadence func() time.Duration
	// progress reports the start of the loop's most recent poll pass, or the
	// zero time when no pass has begun yet.
	progress func() time.Time
	// beat emits one WATCHDOG=1.
	beat func()
	log  *slog.Logger
}

// run samples until ctx ends. It returns ctx.Err() on shutdown, which is the
// only way out: this sampler never gives up on its own, because a sampler that
// stopped would leave systemd watching a process nothing reports for.
func (w loopWatchdog) run(ctx context.Context) error {
	armedAt := time.Now()
	stalled := false
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		last := w.progress()
		if last.IsZero() {
			// No pass has begun at all. Measure staleness from arming, so a
			// loop that never starts polling is caught like one that stopped.
			last = armedAt
		}
		cadence := w.cadence()
		switch age := time.Since(last); {
		case age > cadence:
			if !stalled {
				w.log.Warn("systemd watchdog: run loop has not begun a poll pass within its interval; withholding the heartbeat until it does",
					"stale_for", age.String(), "loop_cadence", cadence.String())
				stalled = true
			}
		default:
			if stalled {
				w.log.Info("systemd watchdog: run loop resumed; heartbeats resumed",
					"last_pass_age", age.String())
				stalled = false
			}
			w.beat()
		}
	}
}
