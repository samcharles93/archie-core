package archied

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// updateReportPollInterval closes the watchdog/startup race: the watchdog
// writes its result only once this process answers /healthz, so a read at
// boot alone can always miss the report describing that very boot.
const updateReportPollInterval = 2 * time.Second

// startUpdateRelay publishes the update watchdog's phase-2 outcome onto the
// activity stream.
//
// The daemon owns this because the daemon owns the update: the watchdog
// restarts archied and leaves its verdict in a file on archied's host, so
// only archied can read it. It previously ran inside the dashboard's own
// Run, which put a host-local file read in the process that is moving off
// the host (archie-core-8cda.5.4). The report reaches an operator the same
// way it always did, as an event -- persisted with every other event and
// delivered to whichever dashboard is watching.
//
// A report is cleared as soon as it is handled, relayed or unreadable, so a
// later boot never retries it and two boots never race to relay one outcome.
func (b *boot) startUpdateRelay(ctx context.Context, path string) {
	if path == "" || b.bus == nil {
		return
	}
	b.relayUpdateReport(path)
	go func() {
		ticker := time.NewTicker(updateReportPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.relayUpdateReport(path)
			}
		}
	}()
}

func (b *boot) relayUpdateReport(path string) {
	report, found, err := releaseupdate.ReadPendingReport(path)
	if err != nil {
		b.log.Warn("read pending update report failed; discarding it", "err", err)
		if clearErr := releaseupdate.ClearPendingReport(path); clearErr != nil {
			b.log.Warn("clear unreadable update report failed", "err", clearErr)
		}
		return
	}
	if !found {
		return
	}
	b.publishEvent(releaseupdate.ReportEvent(report, daemonRunningVersions(b.agentStatus)))
	if err := releaseupdate.ClearPendingReport(path); err != nil {
		b.log.Warn("clear pending update report failed", "err", err)
	}
}
