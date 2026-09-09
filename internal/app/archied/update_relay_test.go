package archied

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// relayBoot is the daemon reduced to what the relay needs: a bus to publish
// on and a logger.
func relayBoot(t *testing.T) (*boot, *events.Sub) {
	t.Helper()
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	sub := bus.Subscribe(4)
	return &boot{bus: bus, log: slog.New(slog.DiscardHandler)}, sub
}

func waitForEvent(t *testing.T, sub *events.Sub) (events.Event, bool) {
	t.Helper()
	select {
	case e := <-sub.C:
		return e, true
	case <-time.After(2 * time.Second):
		return events.Event{}, false
	}
}

// TestRelayUpdateReportPublishesAndClears: the watchdog leaves its verdict in
// a file on the daemon's host, so the daemon reads it and puts the outcome on
// the activity stream. Relaying twice would tell the operator the same update
// finished twice, so the report is cleared as soon as it is handled
// (archie-core-nln7, rehomed by archie-core-8cda.5.4).
func TestRelayUpdateReportPublishesAndClears(t *testing.T) {
	b, sub := relayBoot(t)
	path := filepath.Join(t.TempDir(), "update-report.json")
	report := releaseupdate.Report{
		Channel:     "webui",
		Previous:    map[string]string{releaseupdate.ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{releaseupdate.ComponentDaemon: "1.1.0"},
		HealthCheck: "passed",
	}
	if err := releaseupdate.WritePendingReport(path, report); err != nil {
		t.Fatal(err)
	}

	b.relayUpdateReport(path)

	e, ok := waitForEvent(t, sub)
	if !ok {
		t.Fatal("no event published for a pending update report")
	}
	if e.Kind != events.KindUpdateReport {
		t.Errorf("kind = %q, want %q", e.Kind, events.KindUpdateReport)
	}
	if e.Data["health_check"] != "passed" {
		t.Errorf("health_check = %v, want passed", e.Data["health_check"])
	}
	if _, found, err := releaseupdate.ReadPendingReport(path); err != nil || found {
		t.Errorf("report still present after relaying: found=%v err=%v", found, err)
	}

	// A second pass has nothing left to relay.
	b.relayUpdateReport(path)
	if e, ok := waitForEventQuickly(sub); ok {
		t.Fatalf("a cleared report was relayed again: %+v", e)
	}
}

func waitForEventQuickly(sub *events.Sub) (events.Event, bool) {
	select {
	case e := <-sub.C:
		return e, true
	case <-time.After(100 * time.Millisecond):
		return events.Event{}, false
	}
}

// TestRelayUpdateReportClearsUnreadableReport: a corrupt report must not jam
// every future boot behind it.
func TestRelayUpdateReportClearsUnreadableReport(t *testing.T) {
	b, _ := relayBoot(t)
	path := filepath.Join(t.TempDir(), "update-report.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	b.relayUpdateReport(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt report file still exists after relaying: err = %v", err)
	}
}

func TestRelayUpdateReportWithNoFileIsSilent(t *testing.T) {
	b, sub := relayBoot(t)

	b.relayUpdateReport(filepath.Join(t.TempDir(), "does-not-exist.json"))

	if e, ok := waitForEventQuickly(sub); ok {
		t.Fatalf("published %+v with no report to relay", e)
	}
}

// TestStartUpdateRelayWithoutAPathDoesNothing: a deployment with no phase-2
// reporting configured must not start a poller for a file that will never
// exist.
func TestStartUpdateRelayWithoutAPathDoesNothing(t *testing.T) {
	b, sub := relayBoot(t)

	b.startUpdateRelay(t.Context(), "")

	if e, ok := waitForEventQuickly(sub); ok {
		t.Fatalf("published %+v with no report path configured", e)
	}
}
