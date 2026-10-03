package archied

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/daemon"
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
// the host. The report reaches an operator the same
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

// daemonRunningVersions reports the component versions this process can
// vouch for, for checking a pending update report against (see
// releaseupdate.Report.Verify).
//
// The daemon component is always vouched for from its own build:
// buildinfo.Version is compiled into this binary, so if an installer claims it
// put a version into service and this value disagrees, the installer is
// wrong. buildinfo.Runtime is deliberately NOT used for the agent component --
// it records the agent version archied's own release pipeline stamped, not
// the version an agent container is actually running, and the two diverge in
// exactly the situation this check exists to detect.
//
// The agent component is included only once agentStatus has actually
// observed one running -- self-reported by an archie-agent worker in a
// taskrun.Response (see daemon.AgentStatus), since every archie-agent
// process is task-scoped and ephemeral, not something this process can query
// directly. Before the first task completes, or when agentStatus is nil
// (composition never wired one), the agent component is left out entirely
// so it reports as unchecked rather than as confirmed.
func daemonRunningVersions(agentStatus *daemon.AgentStatus) map[string]string {
	versions := map[string]string{releaseupdate.ComponentDaemon: buildinfo.Version}
	if agentStatus != nil {
		if version, _, ok := agentStatus.Snapshot(); ok {
			versions[releaseupdate.ComponentAgent] = version
		}
	}
	return versions
}
