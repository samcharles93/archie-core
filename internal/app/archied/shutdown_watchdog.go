package archied

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// shutdownGracePeriod is the fixed grace added to the drain window before the
// shutdown watchdog force-exits the process.
const shutdownGracePeriod = 60 * time.Second

// defaultShutdownDrainTimeout is the graceful-shutdown drain window used to
// compute the watchdog leash when no drain timeout is configured. It bounds
// how long the daemon waits for in-flight work to finish before the
// watchdog's backstop applies. The drain-control marker/timeout is a sibling
// effort; when it lands it supplies this value and the leash is recomputed
// from it.
const defaultShutdownDrainTimeout = 60 * time.Second

// shutdownLeash is the total leash the shutdown watchdog allows: the drain
// window plus the grace period. Shutdown normally completes well within it;
// the leash only bites when the graceful-shutdown machinery hangs.
func shutdownLeash() time.Duration {
	return defaultShutdownDrainTimeout + shutdownGracePeriod
}

// shutdownWatchdog force-exits the daemon if graceful shutdown does not
// complete within the leash. It runs in its own goroutine outside the
// daemon's event loop, so it fires even when the graceful-shutdown machinery
// it backstops is itself the thing that is hung. It depends only on the
// process being alive, never on a subsystem's clean shutdown.
type shutdownWatchdog struct {
	leash time.Duration
	log   *slog.Logger
	exit  func(int)
}

// newShutdownWatchdog builds a watchdog that, once shutdown begins, stops the
// process if it does not complete within leash. exit is injectable so tests
// can observe a forced exit without terminating the test process.
func newShutdownWatchdog(leash time.Duration, log *slog.Logger) *shutdownWatchdog {
	return &shutdownWatchdog{leash: leash, log: log, exit: os.Exit}
}

// watch starts the watchdog goroutine. The returned func disarms it: call it
// once graceful shutdown has completed so a shutdown that finishes within the
// leash is never cut short. If that func is never called -- a hung shutdown --
// the watchdog force-exits the process as soon as the leash expires.
//
// The goroutine blocks on a single select pair and never touches the daemon's
// own event loop or shutdown machinery, which is precisely the point: it must
// still be able to run when those are the parts that are stuck.
func (w *shutdownWatchdog) watch(shutdown <-chan struct{}) func() {
	var once sync.Once
	done := make(chan struct{})
	disarm := func() { once.Do(func() { close(done) }) }

	go func() {
		// Wait for graceful shutdown to begin. If the process is exiting for
		// some other reason, done is closed and the watchdog stands down
		// before doing anything.
		select {
		case <-shutdown:
		case <-done:
			return
		}

		timer := time.NewTimer(w.leash)
		defer timer.Stop()

		select {
		case <-timer.C:
			w.log.Error("shutdown watchdog: graceful shutdown did not complete within leash; forcing exit",
				"leash", w.leash.String())
			w.exit(1)
		case <-done:
			// Shutdown completed within the leash; stand down.
		}
	}()

	return disarm
}

// startShutdownWatchdog arms the daemon's shutdown watchdog. It must be called
// before any other cleanup is registered so its disarm runs last in the LIFO
// cleanup chain: the watchdog stands down only after every other subsystem has
// shut down cleanly. A subsystem that hangs never runs the disarm, so the
// watchdog force-exits.
func (b *boot) startShutdownWatchdog(ctx context.Context) {
	disarm := newShutdownWatchdog(shutdownLeash(), b.log).watch(ctx.Done())
	b.addCleanup(disarm)
}
