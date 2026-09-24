package captureintake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// recordingPublisher captures every event.Publish call so a test can assert
// a capture reached the live SSE pipeline without standing up a real
// streaming HTTP connection.
type recordingPublisher struct {
	published []events.Event
}

func (r *recordingPublisher) Publish(_ context.Context, e events.Event) {
	r.published = append(r.published, e)
}

// testReceiver builds a Receiver over a real store, so tests assert on the
// rows a capture actually persisted rather than on a mock's recollection.
// srv keeps the same handle, since ListCaptures reads back what the receiver
// wrote.
type testReceiver struct {
	*Receiver
	// store is the event-capture store the receiver writes through, kept so a
	// test asserts on persisted rows rather than on a mock's recollection.
	store *edastore.Store
}

// Handler serves the receiver's route the way its host process mounts it,
// so path parsing and method routing are exercised, not bypassed.
func (r testReceiver) Handler() http.Handler {
	mux := http.NewServeMux()
	r.Register(mux)
	return mux
}

// captureTestServerWithoutStore builds a Receiver with no capture storage,
// for the "capture not configured" degradation: the route still answers, but
// with 503 rather than persisting a row.
func captureTestServerWithoutStore(t *testing.T) testReceiver {
	t.Helper()
	return testReceiver{Receiver: &Receiver{Log: slog.New(slog.DiscardHandler)}}
}

func captureTestServer(t *testing.T) testReceiver {
	t.Helper()
	s := edastore.OpenTest(t)
	return testReceiver{
		Receiver: &Receiver{
			Log:          slog.New(slog.DiscardHandler),
			Captures:     s,
			MaxBodyBytes: 1024,
			Retention:    7 * 24 * time.Hour,
			MaxEvents:    100,
			Limiter:      webhookguard.NewRateLimiter(1000, 1000, time.Now),
		},
		store: s,
	}
}

// hmacSHA256 returns the GitHub-style "sha256=<hex>" signature for body
// under secret, the same form webhookguard.VerifyHMAC accepts.
func hmacSHA256(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
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
	got, err := srv.store.ListCaptures(t.Context(), 10)
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
// the host process's event pipeline with Kind "capture" so the dashboard's
// event inspector knows to refetch. This does not open a real SSE
// connection -- it asserts what the receiver hands to its Publish hook,
// which is the part this package adds; persistence and fan-out are the
// host process's pipeline (the daemon's bus drain), already tested there.
//
// The published event is deliberately LIGHTWEIGHT (id + source only, no
// body/headers): the events table this feeds (internal/store/events.go) has
// no retention or row-count prune, unlike captured_events, so embedding the
// full (up to CaptureMaxBodyBytes) payload here would duplicate it into an
// unbounded table and defeat the disk-bound guarantee InsertCapture's own
// prune-on-write exists to provide. See docs/prds/event-capture-storage.md.
func TestHandleCapturePublishesLiveEventOnSuccess(t *testing.T) {
	srv := captureTestServer(t)
	pub := &recordingPublisher{}
	srv.Publish = pub.Publish

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
	// e.ID is deliberately not pinned here: assigning the persisted
	// events-table row id is the host pipeline's persistence step, not the
	// receiver's, and the receiver publishes before that runs.
	captured, err := srv.store.ListCaptures(t.Context(), 1)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured rows = %d, want 1", len(captured))
	}
	if got, want := e.Data["id"], captured[0].ID; got != want {
		t.Fatalf("Data[\"id\"] = %v, want %v (the persisted captured_events row id, for frontend dedup)", got, want)
	}
	if got, _ := e.Data["source"].(string); got != "github" {
		t.Fatalf("Data[\"source\"] = %v, want \"github\"", e.Data["source"])
	}
	if _, hasBody := e.Data["body"]; hasBody {
		t.Fatalf("Data[\"body\"] present, want it omitted -- embedding the full payload here duplicates it into the unbounded events table")
	}
	if _, hasHeaders := e.Data["headers"]; hasHeaders {
		t.Fatalf("Data[\"headers\"] present, want it omitted -- same unbounded-duplication risk as body")
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
	got, err := srv.store.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || got[0].Body != "not json" {
		t.Fatalf("captured = %+v, want raw body preserved when it is not JSON", got)
	}
}

func TestHandleCaptureRejectsOversizedBody(t *testing.T) {
	srv := captureTestServer(t)
	srv.MaxBodyBytes = 8
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{"far":"too big for the cap"}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	got, err := srv.store.ListCaptures(t.Context(), 10)
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
	srv.MaxBodyBytes = 0

	oversized := strings.Repeat("x", fallbackMaxBodyBytes+1)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(oversized))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (CaptureMaxBodyBytes=0 must still enforce the fallback cap)", w.Code, http.StatusRequestEntityTooLarge)
	}
	got, err := srv.store.ListCaptures(t.Context(), 10)
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
	srv.Limiter = webhookguard.NewRateLimiter(0, 1, time.Now) // burst 1, no refill

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

	got, err := srv.store.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("captured rows = %d, want 2 (the rate-limited request must not be persisted)", len(got))
	}
}

func TestHandleCaptureIgnoresGetMethod(t *testing.T) {
	// The route is registered "POST /webhooks/capture/{source}"; a GET does
	// not match it and falls through to the host mux's own routing (405 in
	// this isolated registration) -- it must not be treated as a capture.
	srv := captureTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/webhooks/capture/github", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusAccepted {
		t.Fatalf("status = %d, a GET must never be accepted as a capture", w.Code)
	}
	got, err := srv.store.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("captured rows = %d, want 0 (GET must not persist a capture)", len(got))
	}
}

func TestHandleCaptureWithoutCapturesConfiguredIs503(t *testing.T) {
	srv := captureTestServerWithoutStore(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d when Captures is nil", w.Code, http.StatusServiceUnavailable)
	}
}

// captureInsertErrorStore forces InsertCapture to fail so the handler's
// error path is exercised without relying on a real storage failure.
type captureInsertErrorStore struct {
	store.CaptureStore
}

func (captureInsertErrorStore) InsertCapture(context.Context, store.CapturedEvent, time.Duration, int) (string, error) {
	return "", errors.New("boom")
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

// stubSources resolves a fixed set of sources, or fails every lookup.
type stubSources struct {
	byPath map[string]source.Source
	err    error
}

func (s stubSources) GetSource(_ context.Context, path string) (*source.Source, error) {
	if s.err != nil {
		return nil, s.err
	}
	src, ok := s.byPath[path]
	if !ok {
		return nil, nil
	}
	return &src, nil
}

// memCaptures records inserted captures, including the Unsigned flag the
// legacy store does not persist.
type memCaptures struct{ rows []store.CapturedEvent }

func (m *memCaptures) InsertCapture(_ context.Context, c store.CapturedEvent, _ time.Duration, _ int) (string, error) {
	m.rows = append(m.rows, c)
	return "c", nil
}

func (m *memCaptures) ListCaptures(context.Context, int) ([]store.CapturedEvent, error) {
	return m.rows, nil
}

// TestHandleCaptureSigning pins the source signing rule: a signed source
// authenticates only a valid X-Hub-Signature-256 or X-Signature-256 HMAC
// under its secret; an approved unsigned source marks every event unsigned;
// anything else is captured and neither authenticated nor unsigned, so it
// never dispatches.
func TestHandleCaptureSigning(t *testing.T) {
	const secret = "abcdefghijklmnop"
	const body = `{"action":"opened"}`
	signed := source.Source{Path: "github", Signing: source.SigningSigned, Secret: secret}
	pending := source.Source{Path: "github", Signing: source.SigningUnsignedPending, Secret: secret}
	unsigned := source.Source{Path: "github", Signing: source.SigningUnsigned, Secret: secret}
	noSecret := source.Source{Path: "github", Signing: source.SigningSigned}
	tests := []struct {
		name          string
		sources       SourceResolver
		header, sig   string
		authenticated bool
		unsigned      bool
	}{
		{"signed, valid hub signature", sources(signed), "X-Hub-Signature-256", hmacSHA256(secret, body), true, false},
		{"signed, valid fallback signature", sources(signed), "X-Signature-256", hmacSHA256(secret, body), true, false},
		{"signed, wrong signature", sources(signed), "X-Hub-Signature-256", hmacSHA256("not-the-real-secret", body), false, false},
		{"signed, no signature", sources(signed), "", "", false, false},
		{"signed, no secret yet", sources(noSecret), "X-Hub-Signature-256", hmacSHA256("", body), false, false},
		{"unsigned request not yet approved", sources(pending), "", "", false, false},
		{"unsigned request not yet approved, signed event", sources(pending), "X-Hub-Signature-256", hmacSHA256(secret, body), true, false},
		{"approved unsigned", sources(unsigned), "", "", false, true},
		{"unknown source", sources(), "X-Hub-Signature-256", hmacSHA256(secret, body), false, false},
		{"lookup error fails open at capture", stubSources{err: errors.New("db locked")}, "", "", false, false},
		{"no source store", nil, "X-Hub-Signature-256", hmacSHA256(secret, body), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captures := &memCaptures{}
			rc := &Receiver{Log: slog.New(slog.DiscardHandler), Captures: captures, Sources: tt.sources}
			mux := http.NewServeMux()
			rc.Register(mux)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(body))
			if tt.header != "" {
				req.Header.Set(tt.header, tt.sig)
			}
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
			}
			if len(captures.rows) != 1 {
				t.Fatalf("captured rows = %d, want 1", len(captures.rows))
			}
			got := captures.rows[0]
			if got.Authenticated != tt.authenticated || got.Unsigned != tt.unsigned {
				t.Fatalf("authenticated, unsigned = %v, %v; want %v, %v", got.Authenticated, got.Unsigned, tt.authenticated, tt.unsigned)
			}
		})
	}
}

func sources(list ...source.Source) stubSources {
	byPath := map[string]source.Source{}
	for _, s := range list {
		byPath[s.Path] = s
	}
	return stubSources{byPath: byPath}
}
