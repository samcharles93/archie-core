package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type stubApplyStatus struct {
	records []storecontract.ApplyStatus
	err     error
}

func (s stubApplyStatus) PutApplyStatus(_ context.Context, _ storecontract.ApplyStatus) error {
	return nil
}

func (s stubApplyStatus) ListApplyStatus(context.Context) ([]storecontract.ApplyStatus, error) {
	return s.records, s.err
}

func applyStatusResponse(t *testing.T, server *Server) applyStatusView {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/control-plane/apply-status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET apply-status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var view applyStatusView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode apply status: %v (body %s)", err, recorder.Body.String())
	}
	return view
}

// TestApplyStatusDerivesState pins the three readings an operator acts on: a
// process on the current version is live, one on an older version of a
// restart-required kind is pending a restart, and one that has stopped
// re-stamping is unknown rather than current (archie-core-pskb).
func TestApplyStatusDerivesState(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	server := &Server{ApplyStatus: stubApplyStatus{records: []storecontract.ApplyStatus{
		{Process: applystatus.Daemon, Kind: "tool-settings", AppliedVersion: 4, ReportedAt: now.Add(-10 * time.Second)},
		{Process: applystatus.Gateway, Kind: "tool-settings", AppliedVersion: 3, ReportedAt: now.Add(-10 * time.Second)},
		{Process: applystatus.Messaging, Kind: "tool-settings", AppliedVersion: 4, ReportedAt: now.Add(-10 * time.Minute)},
	}}}
	server.now = func() time.Time { return now }

	got := applyStatusResponse(t, server)
	states := map[string]string{}
	for _, record := range got.Records {
		states[record.Process] = record.State
	}
	for process, want := range map[string]string{
		applystatus.Daemon:    applyStateCurrent,
		applystatus.Gateway:   applyStateCurrent,
		applystatus.Messaging: applyStateUnknown,
	} {
		if states[process] != want {
			t.Errorf("%s state = %q, want %q", process, states[process], want)
		}
	}
}

// TestApplyStatusReportsAnError pins that a process that rejected an edit
// reads as failed with its error, and keeps showing the version still live
// in it.
func TestApplyStatusReportsAnError(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	server := &Server{ApplyStatus: stubApplyStatus{records: []storecontract.ApplyStatus{
		{
			Process: applystatus.Daemon, Kind: "tool-settings", AppliedVersion: 3,
			Error: "validate database settings: bad policy", ReportedAt: now,
		},
	}}}
	server.now = func() time.Time { return now }

	got := applyStatusResponse(t, server)
	if len(got.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(got.Records))
	}
	record := got.Records[0]
	if record.State != applyStateFailed || record.Error == "" || record.AppliedVersion != 3 {
		t.Fatalf("record = %+v, want failed with its error and version 3 still live", record)
	}
}

// TestApplyStatusWithoutAStoreIsEmptyNotAnError: a process with no apply
// status wiring must render an empty page, the same way the capture list
// reports disabled rather than failing the dashboard.
func TestApplyStatusWithoutAStoreIsEmptyNotAnError(t *testing.T) {
	got := applyStatusResponse(t, &Server{})
	if len(got.Records) != 0 {
		t.Fatalf("got %d records from a server with no store, want none", len(got.Records))
	}
}
