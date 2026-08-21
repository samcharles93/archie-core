package webui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

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
