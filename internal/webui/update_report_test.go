package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// TestReportPendingUpdateEmitsAndClearsReport is the webui counterpart of
// telegram's TestReportPendingUpdateSendsAndClearsReport: a dashboard-
// initiated install must produce a phase-2 report that reaches the
// dashboard, and must never be relayed twice (archie-core-nln7).
func TestReportPendingUpdateEmitsAndClearsReport(t *testing.T) {
	s := newTestServer(t)
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
	s.UpdateReportPath = path
	s.RunningVersions = func() map[string]string {
		return map[string]string{releaseupdate.ComponentDaemon: "1.1.0"}
	}

	ctx := context.Background()
	s.reportPendingUpdate(ctx)

	evs, err := s.Store.EventsSince(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("events = %#v, want exactly one", evs)
	}
	if evs[0].Kind != events.KindUpdateReport {
		t.Errorf("kind = %q, want %q", evs[0].Kind, events.KindUpdateReport)
	}
	confirmed, _ := evs[0].Data["confirmed"].([]any)
	if len(confirmed) != 1 || confirmed[0] != releaseupdate.ComponentDaemon {
		t.Errorf("confirmed = %#v, want [%q]", evs[0].Data["confirmed"], releaseupdate.ComponentDaemon)
	}
	if !strings.Contains(evs[0].Detail, "confirmed running") {
		t.Errorf("Detail = %q, want it to say confirmed running", evs[0].Detail)
	}

	if _, found, err := releaseupdate.ReadPendingReport(path); err != nil || found {
		t.Errorf("report still present after reportPendingUpdate: found=%v err=%v", found, err)
	}
}

// TestReportPendingUpdateClearsUnreadableReport mirrors the telegram
// behaviour: a corrupt report must not jam every future boot.
func TestReportPendingUpdateClearsUnreadableReport(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(t.TempDir(), "update-report.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.UpdateReportPath = path

	s.reportPendingUpdate(context.Background())

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt report file still exists after reportPendingUpdate: err = %v", err)
	}
}

func TestReportPendingUpdateNoFileIsNoop(t *testing.T) {
	s := newTestServer(t)
	s.UpdateReportPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	s.reportPendingUpdate(context.Background())

	evs, err := s.Store.EventsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("events = %#v, want none", evs)
	}
}

func TestReportPendingUpdateDisabledWhenPathEmpty(t *testing.T) {
	s := newTestServer(t)

	s.reportPendingUpdate(context.Background())

	evs, err := s.Store.EventsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("events = %#v, want none", evs)
	}
}

// TestSummarizeUpdateReportDistinguishesDriftFromConfirmed is the false-
// success regression the whole verification mechanism (c25799e,
// archie-core-3jag's sibling) exists to catch: a passed health probe alone
// must never be worded as a confirmed version.
func TestSummarizeUpdateReportDistinguishesDriftFromConfirmed(t *testing.T) {
	report := releaseupdate.Report{
		HealthCheck: "passed",
		Previous:    map[string]string{releaseupdate.ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{releaseupdate.ComponentDaemon: "1.1.0"},
	}
	running := map[string]string{releaseupdate.ComponentDaemon: "1.0.0"}
	verification := report.Verify(running)

	got := summarizeUpdateReport(report, verification)
	if !strings.Contains(got, "did NOT take effect") {
		t.Errorf("summary = %q, want it to flag drift rather than claim success", got)
	}
	if strings.Contains(got, "confirmed running") {
		t.Errorf("summary = %q, must not claim confirmed running when drift was detected", got)
	}
}

// TestHandleChatUpdateInstallSetsReportPath is the regression proof: before
// this fix, the webui install handler passed
// releaseupdate.InstallMeta{Channel: "webui"} with no ReportPath, so the
// watchdog's write_report() (scripts/archie-update-watchdog) silently
// returned without ever writing a phase-2 report.
func TestHandleChatUpdateInstallSetsReportPath(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "update-report.json")
	updates := &chatUpdateStub{snapshot: releaseupdate.Snapshot{
		Components: []releaseupdate.Component{{ID: "gateway", Label: "Gateway", Installed: "1.0", Available: "1.1"}},
	}}
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	server := &Server{Chat: &ChatService{Router: router, Sessions: sessions, Updates: updates}, UpdateReportPath: reportPath}

	body, err := json.Marshal(chatUpdateRequest{Snapshot: updates.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/update/install", bytes.NewReader(body)))
	if res.Code != http.StatusOK || !updates.installed {
		t.Fatalf("update install = %d, body = %s", res.Code, res.Body.String())
	}
	if updates.installedMeta.ReportPath != reportPath {
		t.Errorf("ReportPath = %q, want %q", updates.installedMeta.ReportPath, reportPath)
	}
}
