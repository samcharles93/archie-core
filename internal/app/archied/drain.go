package archied

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/drain"
	drainio "github.com/samcharles93/archie-core/internal/infrastructure/drain"
)

// defaultDrainPollInterval is how often the daemon looks for a new drain
// request. It is deliberately short (a few seconds) so an operator-triggered
// drain is honoured promptly, while the cost is one cheap file read per tick.
const defaultDrainPollInterval = 5 * time.Second

// DefaultDrainRequestPath returns the marker file path the daemon watches. It
// lives under the repo's config home (${XDG_CONFIG_HOME:-~/.config}/archie) --
// the established convention for archied's per-user paths -- rather than the
// epic's literal ~/.archie, which is not a path this daemon ever uses. The
// discrepancy is intentional: every other archied path reads from the XDG
// config home, so the drain marker must too, or an operator pointing at the
// documented location would miss it.
func DefaultDrainRequestPath() string {
	return filepath.Join(configHome(), "archie", drainio.DefaultMarkerFilename)
}

// monitorDrainRequests polls check for a live drain request and invokes
// shutdown when one is honoured. It returns nil once shutdown has been
// triggered (or when the context is cancelled), and never returns a poll error:
// a transient read failure is logged and ignored so a broken marker or a
// momentary read error cannot halt the monitor and silently unmount drain
// detection.
//
// check reports the drain decision; shutdown is the daemon's graceful-stop
// trigger. Both are injected so tests can exercise the monitor without touching
// the real filesystem or signalling the process.
func monitorDrainRequests(
	ctx context.Context,
	check func() (drain.Decision, error),
	shutdown func(),
	log *slog.Logger,
	interval time.Duration,
) error {
	for {
		decision, err := check()
		switch {
		case err != nil:
			log.Warn("drain request check failed", "err", err)
		case decision == drain.DecisionValid:
			log.Info("drain request accepted; initiating graceful shutdown")
			shutdown()
			return nil
		case decision == drain.DecisionStale:
			log.Warn("stale drain request ignored; marker predates current instantiation")
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// startDrainMonitor launches the drain-monitoring goroutine for the long-lived
// daemon. The marker path is resolved from the repo's config home; the reader
// uses the real current instantiation epoch; and a valid request cancels the
// boot context, which is the exact path a SIGTERM takes to graceful shutdown.
//
// It is only called from the non-once run loop, so the monitor never runs for
// a single-cycle invocation.
func (b *boot) startDrainMonitor(ctx context.Context) {
	if b.shutdown == nil {
		b.log.Error("drain monitor not started: no shutdown trigger wired")
		return
	}
	reader := drainio.New(DefaultDrainRequestPath(), nil)
	go func() {
		err := monitorDrainRequests(ctx,
			func() (drain.Decision, error) { return reader.Check() },
			func() { b.shutdown() },
			b.log,
			defaultDrainPollInterval,
		)
		if err != nil && !errors.Is(err, context.Canceled) {
			b.log.Error("drain monitor stopped", "err", err)
		}
	}()
}
