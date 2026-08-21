package webui

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// recordingPublisher captures every event.Publish call so a test can assert
// a capture reached the live SSE pipeline without standing up a real
// streaming HTTP connection.
type recordingPublisher struct {
	published []events.Event
}

func (r *recordingPublisher) Publish(e events.Event) {
	r.published = append(r.published, e)
}

// captureTestServer builds a Server whose Captures field points at the same
// concrete *store.Store as Store, so tests can assert on persisted rows
// through the narrower CaptureStore interface -- Server.Store's static type
// (store.TaskStore) does not expose InsertCapture/ListCaptures.
func captureTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{
		Store:               s,
		Log:                 slog.New(slog.DiscardHandler),
		Captures:            s,
		CaptureMaxBodyBytes: 1024,
		CaptureRetention:    7 * 24 * time.Hour,
		CaptureMaxEvents:    100,
		CaptureLimiter:      webhookguard.NewRateLimiter(1000, 1000, time.Now),
	}
}

func TestHandleCaptureStoresRedactedBody(t *testing.T) {
	srv := captureTestServer(t)
	body := `{"action":"opened","sender":{"token":"shh-secret"}}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusAccepted, w.Body.String())
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("captured rows = %d, want 1", len(got))
	}
	c := got[0]
	if c.Source != "github" {
		t.Errorf("Source = %q, want %q", c.Source, "github")
	}
	if c.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", c.ContentType)
	}
	if strings.Contains(c.Body, "shh-secret") {
		t.Errorf("Body = %q, want the token value redacted", c.Body)
	}
	if !strings.Contains(c.Body, `"action":"opened"`) {
		t.Errorf("Body = %q, want the non-sensitive field preserved", c.Body)
	}
}

// TestHandleCapturePublishesLiveEventOnSuccess pins the "no manual refresh"
// acceptance criterion's actual mechanism: a successful capture must reach
// the existing operator-activity event pipeline (Server.emit -> Events.Publish)
// with Kind "capture" and the same field shape GET /api/captures returns, so
// the dashboard's event inspector can prepend it directly. This does not open
// a real SSE connection -- it asserts what handleCapture hands to the
// publisher, which is the part this feature actually adds; Broadcast/handleSSE
// themselves are pre-existing and separately tested.
func TestHandleCapturePublishesLiveEventOnSuccess(t *testing.T) {
	srv := captureTestServer(t)
	pub := &recordingPublisher{}
	srv.Events = pub

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{"action":"opened"}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published events = %d, want 1", len(pub.published))
	}
	e := pub.published[0]
	if e.Kind != "capture" {
		t.Fatalf("Kind = %q, want \"capture\"", e.Kind)
	}
	if e.ID == 0 {
		t.Fatalf("ID = 0, want the persisted events-table row id")
	}
	if got, _ := e.Data["source"].(string); got != "github" {
		t.Fatalf("Data[\"source\"] = %v, want \"github\"", e.Data["source"])
	}
	body, _ := e.Data["body"].(string)
	if !strings.Contains(body, `"action":"opened"`) {
		t.Fatalf("Data[\"body\"] = %q, want the captured payload", body)
	}
}

func TestHandleCaptureAcceptsUnauthenticatedByDesign(t *testing.T) {
	// Capture must accept requests with no HMAC secret configured -- that is
	// the entire point of an unbound capture endpoint. This test documents
	// that the handler does not gate on any signature header.
	srv := captureTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/unknown-source", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
}

func TestHandleCaptureNonJSONBodyStoredUnredacted(t *testing.T) {
	srv := captureTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/plain", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || got[0].Body != "not json" {
		t.Fatalf("captured = %+v, want raw body preserved when it is not JSON", got)
	}
}

func TestHandleCaptureRejectsOversizedBody(t *testing.T) {
	srv := captureTestServer(t)
	srv.CaptureMaxBodyBytes = 8
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{"far":"too big for the cap"}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("captured rows = %d, want 0 (oversized body must never be persisted)", len(got))
	}
}

// TestHandleCaptureEnforcesBodyCapEvenWhenUnconfigured pins that a caller
// that forgets to set CaptureMaxBodyBytes (zero value) still gets a bound --
// this is the one disk-bound mechanic that limits a single payload's size
// rather than row count or write rate, so it must never silently no-op.
func TestHandleCaptureEnforcesBodyCapEvenWhenUnconfigured(t *testing.T) {
	srv := captureTestServer(t)
	srv.CaptureMaxBodyBytes = 0

	oversized := strings.Repeat("x", fallbackCaptureMaxBodyBytes+1)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(oversized))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (CaptureMaxBodyBytes=0 must still enforce the fallback cap)", w.Code, http.StatusRequestEntityTooLarge)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("captured rows = %d, want 0", len(got))
	}
}

func TestHandleCaptureRateLimitsPerRemoteAddr(t *testing.T) {
	// Deliberately NOT keyed by the source path segment: source is an
	// unregistered, attacker-chosen string for this endpoint (see
	// handleCapture and docs/prds/event-capture-storage.md), so both
	// requests below use the SAME source and DIFFERENT RemoteAddr to prove
	// the limiter is keyed on the sender's address, not the segment.
	srv := captureTestServer(t)
	srv.CaptureLimiter = webhookguard.NewRateLimiter(0, 1, time.Now) // burst 1, no refill

	req := func(remoteAddr string) *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
		r.RemoteAddr = remoteAddr
		return r
	}

	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, req("203.0.113.5:11111"))
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first request status = %d, want %d", w1.Code, http.StatusAccepted)
	}

	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req("203.0.113.5:22222"))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (same host, different port) status = %d, want %d", w2.Code, http.StatusTooManyRequests)
	}

	// A different remote address, same source segment, must not be affected
	// by the exhausted budget above.
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req("198.51.100.9:33333"))
	if w3.Code != http.StatusAccepted {
		t.Fatalf("other-remote-addr request status = %d, want %d", w3.Code, http.StatusAccepted)
	}

	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("captured rows = %d, want 2 (the rate-limited request must not be persisted)", len(got))
	}
}

func TestHandleCaptureIgnoresGetMethod(t *testing.T) {
	// The route is registered "POST /webhooks/capture/{source}"; a GET does
	// not match it and falls through to the dashboard's own routing, same as
	// every other POST-only API route in this server -- it must not be
	// treated as a capture.
	srv := captureTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/webhooks/capture/github", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusAccepted {
		t.Fatalf("status = %d, a GET must never be accepted as a capture", w.Code)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("captured rows = %d, want 0 (GET must not persist a capture)", len(got))
	}
}

func TestHandleCaptureWithoutCapturesConfiguredIs503(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d when Captures is nil", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCaptureBypassesTokenAuth(t *testing.T) {
	// The capture endpoint must accept unauthenticated senders even when the
	// dashboard itself is token-gated -- mirrors /healthz's precedent.
	srv := captureTestServer(t)
	srv.Token = "dashboard-secret"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (capture must bypass requireToken)", w.Code, http.StatusAccepted)
	}
}

// captureInsertErrorStore forces InsertCapture to fail so the handler's
// error path is exercised without relying on a real storage failure.
type captureInsertErrorStore struct {
	store.CaptureStore
}

func (captureInsertErrorStore) InsertCapture(context.Context, store.CapturedEvent, time.Duration, int) (int64, error) {
	return 0, errors.New("boom")
}

func TestHandleCaptureStorageFailureIs500(t *testing.T) {
	srv := captureTestServer(t)
	srv.Captures = captureInsertErrorStore{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
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
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/"+src, strings.NewReader(`{"n":1}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("seed capture %q: status = %d", src, w.Code)
		}
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
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/src", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
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
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/src", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
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
