package releaseupdate

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
)

// TestReportEventDistinguishesDriftFromConfirmed is the false-success
// regression the whole verification mechanism exists to catch: a passed
// health probe alone must never be worded as a confirmed version.
func TestReportEventDistinguishesDriftFromConfirmed(t *testing.T) {
	report := Report{
		HealthCheck: "passed",
		Previous:    map[string]string{ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{ComponentDaemon: "1.1.0"},
	}

	drifted := ReportEvent(report, map[string]string{ComponentDaemon: "1.0.0"})
	if drifted.Kind != events.KindUpdateReport {
		t.Fatalf("kind = %q, want %q", drifted.Kind, events.KindUpdateReport)
	}
	if !strings.Contains(drifted.Detail, "did NOT take effect") {
		t.Errorf("summary = %q, want it to flag drift rather than claim success", drifted.Detail)
	}
	if strings.Contains(drifted.Detail, "confirmed running") {
		t.Errorf("summary = %q, must not claim confirmed running when drift was detected", drifted.Detail)
	}

	confirmed := ReportEvent(report, map[string]string{ComponentDaemon: "1.1.0"})
	if !strings.Contains(confirmed.Detail, "confirmed running") {
		t.Errorf("summary = %q, want it to confirm the running version", confirmed.Detail)
	}
	ids, _ := confirmed.Data["confirmed"].([]string)
	if len(ids) != 1 || ids[0] != ComponentDaemon {
		t.Errorf("confirmed = %#v, want [%q]", confirmed.Data["confirmed"], ComponentDaemon)
	}
}

// TestReportEventRolledBackIsNeverWordedAsSuccess.
func TestReportEventRolledBackIsNeverWordedAsSuccess(t *testing.T) {
	report := Report{
		HealthCheck: "failed",
		RolledBack:  true,
		Previous:    map[string]string{ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{ComponentDaemon: "1.1.0"},
	}

	e := ReportEvent(report, map[string]string{ComponentDaemon: "1.0.0"})
	if !strings.Contains(e.Detail, "rolled back") {
		t.Errorf("summary = %q, want it to report the rollback", e.Detail)
	}
	if e.Data["rolled_back"] != true {
		t.Errorf("rolled_back = %v, want true", e.Data["rolled_back"])
	}
}
