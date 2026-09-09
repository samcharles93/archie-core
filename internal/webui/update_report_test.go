package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

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
	server := &Server{Chat: testChatService(router, sessions, nil, nil, nil, updates, nil), UpdateReportPath: reportPath}

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

func TestHandleChatUpdateInstallRoutesRestartReportToTelegram(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "telegram-update-report.json")
	updates := &chatUpdateStub{snapshot: releaseupdate.Snapshot{
		Components: []releaseupdate.Component{{ID: "gateway", Label: "Gateway", Installed: "1.0", Available: "1.1"}},
	}}
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	server := &Server{
		Chat:                     testChatService(router, sessions, nil, nil, nil, updates, nil),
		UpdateReportPath:         filepath.Join(t.TempDir(), "webui-update-report.json"),
		TelegramUpdateReportPath: reportPath,
		TelegramUpdateChatID:     42,
	}

	body, err := json.Marshal(chatUpdateRequest{Snapshot: updates.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/update/install", bytes.NewReader(body)))
	if res.Code != http.StatusOK || !updates.installed {
		t.Fatalf("update install = %d, body = %s", res.Code, res.Body.String())
	}
	if updates.installedMeta.Channel != "telegram" {
		t.Errorf("Channel = %q, want telegram", updates.installedMeta.Channel)
	}
	if updates.installedMeta.ChatID != 42 {
		t.Errorf("ChatID = %d, want 42", updates.installedMeta.ChatID)
	}
	if updates.installedMeta.ReportPath != reportPath {
		t.Errorf("ReportPath = %q, want %q", updates.installedMeta.ReportPath, reportPath)
	}
}
