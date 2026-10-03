// sd_notify.go wires the daemon into internal/sdnotify's implementation of
// systemd's sd_notify(3) protocol: one READY=1 when the run loop starts
// serving, and a WATCHDOG=1 heartbeat while the run loop is demonstrably still
// making progress.
//
// The protocol client and the heartbeat sampler live in internal/sdnotify
// because archie-messaging, a separate process that cannot import this
// package, is the next process that must be able to announce itself.
// What stays here is what is the daemon's own: which
// marker counts as progress (daemon.LastPollAt) and how long the loop may go
// without beginning a poll pass.
//
// Nothing here is fatal -- a process with no NOTIFY_SOCKET (a plain terminal,
// Docker, a user unit without WatchdogSec) sends nothing and serves exactly as
// before, and a socket that cannot be written to is logged and ignored.
//
// Consuming the states is the unit's business: READY=1 needs Type=notify, and
// the heartbeat needs WatchdogSec.
package archied

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/samcharles93/archie-core/internal/sdnotify"
)

// announceReady tells systemd the daemon has finished starting. It is called at
// the moment a process declares itself serving: the daemon's run loop (next to
// healthSurface.markServing), and the serve steps of archie-gateway and
// archie-state-store, which have health surfaces of their
// own and would otherwise hang a Type=notify unit until TimeoutStartSec while
// perfectly healthy.
func (b *boot) announceReady() {
	sdnotify.Ready(b.log)
}

// startWatchdog arms the heartbeat sampler for the daemon's run loop. It does
// nothing unless a manager named a notify socket and asked for a watchdog
// (WATCHDOG_USEC), so every deployment without systemd is unaffected.
func (b *boot) startWatchdog(ctx context.Context) {
	n := sdnotify.New(os.Getenv, b.log)
	if !n.Enabled() {
		return
	}
	interval, ok := sdnotify.Heartbeat(os.Getenv, os.Getpid(), b.log)
	if !ok {
		return
	}

	sampler := sdnotify.Watchdog{
		Interval: interval,
		Cadence:  b.loopCadence,
		Progress: b.d.LastPollAt,
		Beat:     func() { n.Send(sdnotify.WatchdogState) },
		Log:      b.log,
	}
	b.log.Info("systemd watchdog armed",
		"heartbeat", interval.String(),
		"loop_cadence", b.loopCadence().String())

	go func() {
		if err := sampler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
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
