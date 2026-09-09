package webui

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/store"
)

// captureTestServer builds a Server whose Captures field points at the same
// concrete *store.Store as Store, so tests can assert on persisted rows
// through the narrower CaptureStore interface -- Server.Store's static type
// (store.TaskStore) does not expose ListCaptures.
func captureTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{
		Store:    s,
		Log:      slog.New(slog.DiscardHandler),
		Captures: s,
	}
}

// seedCapture inserts a captured row directly through the CaptureStore.
// These tests cover the list read; the write path itself lives in
// internal/infrastructure/captureintake and is tested there.
func seedCapture(t *testing.T, srv *Server, source string) int64 {
	t.Helper()
	id, err := srv.Captures.InsertCapture(t.Context(), store.CapturedEvent{
		ReceivedAt: time.Now().UTC(),
		Source:     source,
		Body:       `{"n":1}`,
	}, 7*24*time.Hour, 100)
	if err != nil {
		t.Fatalf("seed capture %q: %v", source, err)
	}
	return id
}

func captureListResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, w.Body.String())
	}
	return body
}

func TestHandleCapturesListsRecentCapturesNewestFirst(t *testing.T) {
	srv := captureTestServer(t)
	for _, src := range []string{"first", "second", "third"} {
		seedCapture(t, srv, src)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/captures", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	body := captureListResponse(t, w)
	if enabled, _ := body["enabled"].(bool); !enabled {
		t.Fatalf("enabled = %v, want true", body["enabled"])
	}
	captures, ok := body["captures"].([]any)
	if !ok || len(captures) != 3 {
		t.Fatalf("captures = %#v, want 3 entries", body["captures"])
	}
	first, ok := captures[0].(map[string]any)
	if !ok || first["source"] != "third" {
		t.Fatalf("captures[0] = %#v, want newest-first (source=\"third\")", captures[0])
	}
}

func TestHandleCapturesRespectsLimitQueryParam(t *testing.T) {
	srv := captureTestServer(t)
	for range 3 {
		seedCapture(t, srv, "src")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/captures?limit=1", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	body := captureListResponse(t, w)
	captures, _ := body["captures"].([]any)
	if len(captures) != 1 {
		t.Fatalf("captures = %#v, want 1 entry (limit=1)", body["captures"])
	}
}

func TestHandleCapturesDefaultsLimitWhenMissingOrInvalid(t *testing.T) {
	srv := captureTestServer(t)
	for range 3 {
		seedCapture(t, srv, "src")
	}

	for _, limit := range []string{"", "not-a-number", "-5", "0"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/captures?limit="+limit, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)

		body := captureListResponse(t, w)
		captures, _ := body["captures"].([]any)
		if len(captures) != 3 {
			t.Fatalf("limit=%q: captures = %#v, want all 3 (an invalid/absent limit must not become a 0-row LIMIT)", limit, body["captures"])
		}
	}
}

func TestHandleCapturesWithoutCapturesConfiguredIsEnabledFalse(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/captures", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (unconfigured is not an error, per the memory/skills precedent)", w.Code, http.StatusOK)
	}
	body := captureListResponse(t, w)
	if enabled, _ := body["enabled"].(bool); enabled {
		t.Fatalf("enabled = %v, want false when Captures is nil", body["enabled"])
	}
	captures, ok := body["captures"].([]any)
	if !ok || len(captures) != 0 {
		t.Fatalf("captures = %#v, want an empty list", body["captures"])
	}
}

func TestHandleCapturesRequiresToken(t *testing.T) {
	srv := captureTestServer(t)
	srv.Token = "dashboard-secret"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/captures", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("status = %d, want captures to require the dashboard token like every other /api/* route", w.Code)
	}
}

// recordingIntake stands in for the host process's capture receiver
// (internal/infrastructure/captureintake.Receiver): the write path is owned
// elsewhere, so the mount test only needs an http.Handler that answers.
type recordingIntake struct {
	hit int
}

func (r *recordingIntake) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r.hit++
	w.WriteHeader(http.StatusAccepted)
}

// TestCaptureIntakeMountedOnBypassMux pins the mount contract that used to
// live inside the write handler: the capture route must accept
// unauthenticated senders even when the dashboard is token-gated, mirroring
// /healthz's precedent. The receiver decides nothing about tokens; it is
// this registration that keeps intake off requireToken.
func TestCaptureIntakeMountedOnBypassMux(t *testing.T) {
	srv := captureTestServer(t)
	srv.Token = "dashboard-secret"
	intake := &recordingIntake{}
	srv.CaptureIntake = intake

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (intake must bypass requireToken; body = %s)", w.Code, http.StatusAccepted, w.Body.String())
	}
	if intake.hit != 1 {
		t.Fatalf("intake handler calls = %d, want 1", intake.hit)
	}
}

// TestCaptureIntakeNilLeavesRouteAbsent pins the composition lever: a
// process that does not own intake (the UI process, per compose) leaves
// CaptureIntake nil and gets no capture route at all, rather than an
// intake that 503s. Two listeners serving the same authority is what
// docs/prds/ui-service-boundary.md:30-33 forbids.
func TestCaptureIntakeNilLeavesRouteAbsent(t *testing.T) {
	srv := captureTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusAccepted {
		t.Fatalf("status = %d, want any non-accept (the route must not exist without a mounted intake)", w.Code)
	}
}
