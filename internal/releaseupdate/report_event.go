package releaseupdate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samcharles93/archie-core/internal/events"
)

// ReportEvent turns a Report and its verification into a dashboard event.
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

// summarizeReport returns a one-line summary: confirmed, drifted, unverified
// or rolled back.
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
