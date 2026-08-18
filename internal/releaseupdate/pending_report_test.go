package releaseupdate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPendingReportRoundTrip covers the cross-restart handoff: the watchdog
// script decides the phase-2 outcome and writes it; whichever archied
// process boots next reads it, relays it, and clears it so it is never
// reported twice.
func TestPendingReportRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-report.json")
	want := Report{
		Channel:     "telegram",
		ChatID:      42,
		ThreadID:    7,
		Previous:    map[string]string{"daemon": "1.0.0", "agent": "1.0.0"},
		Installed:   map[string]string{"daemon": "1.1.0", "agent": "1.0.0"},
		HealthCheck: "passed",
		RolledBack:  false,
	}

	if err := WritePendingReport(path, want); err != nil {
		t.Fatalf("WritePendingReport() error = %v", err)
	}

	got, found, err := ReadPendingReport(path)
	if err != nil {
		t.Fatalf("ReadPendingReport() error = %v", err)
	}
	if !found {
		t.Fatal("ReadPendingReport() found = false, want true")
	}
	if got.Channel != want.Channel || got.ChatID != want.ChatID || got.ThreadID != want.ThreadID ||
		got.HealthCheck != want.HealthCheck || got.RolledBack != want.RolledBack ||
		got.Installed["daemon"] != want.Installed["daemon"] || got.Previous["daemon"] != want.Previous["daemon"] {
		t.Fatalf("ReadPendingReport() = %#v, want %#v", got, want)
	}

	if err := ClearPendingReport(path); err != nil {
		t.Fatalf("ClearPendingReport() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("report file still exists after ClearPendingReport(): err = %v", err)
	}
}

// TestReadPendingReportMissingFileReturnsNotFound: no update in flight is
// the overwhelmingly common case (every normal startup), so it must be a
// plain "not found", never an error a caller has to specially ignore.
func TestReadPendingReportMissingFileReturnsNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	_, found, err := ReadPendingReport(path)
	if err != nil {
		t.Fatalf("ReadPendingReport() error = %v, want nil", err)
	}
	if found {
		t.Fatal("ReadPendingReport() found = true, want false")
	}
}

// TestClearPendingReportMissingFileIsNotAnError: startup may race a report
// that was already cleared by a previous boot attempt; clearing twice must
// be harmless.
func TestClearPendingReportMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	if err := ClearPendingReport(path); err != nil {
		t.Fatalf("ClearPendingReport() error = %v, want nil for a missing file", err)
	}
}

func TestWritePendingReportRejectsRolledBackWithoutHealthCheckFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-report.json")
	bad := Report{Channel: "telegram", ChatID: 1, RolledBack: true, HealthCheck: "passed"}
	if err := WritePendingReport(path, bad); err == nil {
		t.Fatal("WritePendingReport() error = nil, want a validation error for rolled_back+passed")
	}
}
