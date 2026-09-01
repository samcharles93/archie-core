package webui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// reportPendingUpdate checks for a phase-2 outcome left by the update
// watchdog for a dashboard-initiated install and relays it onto the
// activity stream, mirroring internal/channels/telegram/update.go's
// reportPendingUpdate for the Telegram channel. Called once on every daemon
// boot (Server.Run), so it fires whether this is the restart the update
// itself triggered or a later, unrelated restart.
//
// A report is cleared as soon as it's handled -- relayed, or found
// unreadable -- so a later boot never retries it and two boots never race
// to relay the same outcome.
func (s *Server) reportPendingUpdate(ctx context.Context) {
	s.updateReportMu.Lock()
	defer s.updateReportMu.Unlock()
	if s.UpdateReportPath == "" {
		return
	}
	report, found, err := releaseupdate.ReadPendingReport(s.UpdateReportPath)
	if err != nil {
		s.logf("read pending update report failed; discarding it", "err", err)
		if clearErr := releaseupdate.ClearPendingReport(s.UpdateReportPath); clearErr != nil {
			s.logf("clear unreadable update report failed", "err", clearErr)
		}
		return
	}
	if !found {
		return
	}
	s.emit(ctx, updateReportEvent(report, s.runningVersions()))
	if err := releaseupdate.ClearPendingReport(s.UpdateReportPath); err != nil {
		s.logf("clear pending update report failed", "err", err)
	}
}

// runningVersions returns the versions components report for themselves, or
// nil when the composition root wired none -- see the identical seam on
// internal/channels/telegram.Gateway. Nil is not a no-op: with nothing to
// check against, every claim comes back Unverified rather than Confirmed.
func (s *Server) runningVersions() map[string]string {
	if s.RunningVersions == nil {
		return nil
	}
	return s.RunningVersions()
}

// updateReportEvent turns a phase-2 Report, plus its verification against
// the versions actually running, into the activity-stream event the
// dashboard renders. Unlike Telegram's formatPendingReport, this ships the
// structured Verification rather than pre-rendered prose, so the frontend
// controls presentation -- the two channels' operators read the same facts
// through different UIs, not necessarily the same sentence.
func updateReportEvent(report releaseupdate.Report, running map[string]string) events.Event {
	verification := report.Verify(running)
	drift := make([]map[string]any, 0, len(verification.Drift))
	for _, d := range verification.Drift {
		drift = append(drift, map[string]any{
			"id":                  d.ID,
			"claimed":             d.Claimed,
			"running":             d.Running,
			"running_is_previous": d.RunningIsPrevious,
		})
	}
	return events.Event{
		Kind:   events.KindUpdateReport,
		Detail: summarizeUpdateReport(report, verification),
		Data: map[string]any{
			"health_check": report.HealthCheck,
			"rolled_back":  report.RolledBack,
			"previous":     report.Previous,
			"installed":    report.Installed,
			"confirmed":    verification.Confirmed,
			"drift":        drift,
			"unverified":   verification.Unverified,
		},
	}
}

// summarizeUpdateReport is the one-line summary shown in the dashboard's
// activity feed, which renders event.Detail generically for every event
// kind (see ui/src/dashboard/dashboard.js activityRow). It condenses the
// same facts internal/channels/telegram/update.go's formatPendingReport
// spells out at length -- not a byte-identical wording, since the two
// surfaces render differently, but the same four outcomes: confirmed,
// drifted, unverified, or rolled back.
func summarizeUpdateReport(report releaseupdate.Report, verification releaseupdate.Verification) string {
	if report.RolledBack {
		return "Update failed its health check and was rolled back to the previous version."
	}
	if report.HealthCheck != "passed" {
		return "Update failed its health check and could NOT be rolled back automatically -- manual intervention required."
	}
	if len(verification.Drift) > 0 {
		ids := make([]string, 0, len(verification.Drift))
		for _, d := range verification.Drift {
			ids = append(ids, fmt.Sprintf("%s reports %s, not the installed %s", d.ID, d.Running, d.Claimed))
		}
		sort.Strings(ids)
		return "Update did NOT take effect: " + strings.Join(ids, "; ") + "."
	}
	if len(verification.Confirmed) == 0 {
		return "Update installed and health check passed, but the running version could not be verified -- please check manually before assuming the update worked."
	}
	text := "Update complete. Health check passed -- confirmed running on the new version (" + strings.Join(verification.Confirmed, ", ") + ")."
	if len(verification.Unverified) > 0 {
		text += " Not independently checked: " + strings.Join(verification.Unverified, ", ") + "."
	}
	return text
}
