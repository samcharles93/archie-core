package releaseupdate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samcharles93/archie-core/internal/events"
)

// ReportEvent turns a phase-2 Report, plus its verification against
// the versions actually running, into the activity-stream event the
// dashboard renders. Unlike Telegram's formatPendingReport, this ships the
// structured Verification rather than pre-rendered prose, so the frontend
// controls presentation -- the two channels' operators read the same facts
// through different UIs, not necessarily the same sentence.
func ReportEvent(report Report, running map[string]string) events.Event {
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
		Detail: summarizeReport(report, verification),
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

// summarizeReport is the one-line summary shown in the dashboard's
// activity feed, which renders event.Detail generically for every event
// kind (see ui/src/dashboard/dashboard.js activityRow). It condenses the
// same facts internal/channels/telegram/update.go's formatPendingReport
// spells out at length -- not a byte-identical wording, since the two
// surfaces render differently, but the same four outcomes: confirmed,
// drifted, unverified, or rolled back.
func summarizeReport(report Report, verification Verification) string {
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
