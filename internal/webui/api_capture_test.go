package webui

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
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

// stubBindingStore injects a configurable armed-binding lookup result so
// handleCapture tests can exercise the single-binding HMAC path and the
// belt-and-braces 409-on-multiple path without seeding the real store
// (which would either run into the overlap-rejection guard or require
// bypassing the draft -> pending_approval -> armed lifecycle). The 409
// path exists exactly for the case where the write-time check was
// bypassed somehow, so simulating that case directly is the most honest
// way to cover it.
type stubBindingStore struct {
	armedBySource map[string][]binding.Binding
	lookupErr     error
}

func (s *stubBindingStore) ArmedBindingsForSource(_ context.Context, source string) ([]binding.Binding, error) {
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return s.armedBySource[source], nil
}

// handleCapture only touches ArmedBindingsForSource on the binding
// surface. The rest of BindingStore is stubbed so the type satisfies the
// interface and Phase C's other handlers can be exercised elsewhere.
func (s *stubBindingStore) InsertBinding(_ context.Context, _ binding.Binding) (int64, error) {
	return 0, nil
}

func (s *stubBindingStore) GetBinding(_ context.Context, _ int64) (*binding.Binding, error) {
	return nil, nil
}

func (s *stubBindingStore) ListBindings(_ context.Context) ([]binding.Binding, error) {
	return nil, nil
}

func (s *stubBindingStore) UpdateBinding(_ context.Context, _ binding.Binding) error {
	return nil
}

func (s *stubBindingStore) DeleteBinding(_ context.Context, _ int64) error {
	return nil
}

func (s *stubBindingStore) ApproveBinding(_ context.Context, _ int64) error {
	return nil
}

func (s *stubBindingStore) RecordDispatch(
	_ context.Context,
	_ *sql.Tx,
	_ int64,
	_ int64,
	_ int64,
	_ int64,
) error {
	return nil
}

func (s *stubBindingStore) ListUndispatchedCaptures(_ context.Context, _ []string, _ int) ([]store.CapturedEvent, error) {
	return nil, nil
}

// captureBindingServer builds a Server with both a real CaptureStore (so
// ListCaptures can read back what handleCapture persisted) and a stub
// BindingDispatcher configured with one armed binding for the given
// source. The mapping/workflow fields are placeholders -- handleCapture
// never reads them, only the Secret.
func captureBindingServer(t *testing.T, secret, source string) *Server {
	t.Helper()
	srv := captureTestServer(t)
	srv.BindingDispatcher = &stubBindingStore{
		armedBySource: map[string][]binding.Binding{
			source: {{
				ID:        1,
				Name:      "test",
				Matcher:   binding.Matcher{Source: source},
				MappingID: 1,
				Workflow:  "implement",
				Version:   1,
				Status:    binding.StatusArmed,
				Secret:    secret,
			}},
		},
	}
	return srv
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
// with Kind "capture" so the dashboard's event inspector knows to refetch.
// This does not open a real SSE connection -- it asserts what handleCapture
// hands to the publisher, which is the part this feature actually adds;
// Broadcast/handleSSE themselves are pre-existing and separately tested.
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
	captured, err := srv.Captures.ListCaptures(t.Context(), 1)
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

// TestHandleCaptureMarksAuthenticatedWhenHMACMatches is the green half of
// the t2db.5 point 1 wiring: a capture POST signed with the armed
// binding's secret must be recorded with Authenticated=true so the
// dispatch loop's binding.Matcher.Matches check (which gates on
// authenticated) can fire.
func TestHandleCaptureMarksAuthenticatedWhenHMACMatches(t *testing.T) {
	const secret = "abcdefghijklmnop" // exactly 16 bytes (binding.Validate floor)
	srv := captureBindingServer(t, secret, "github")
	body := `{"action":"opened"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", hmacSHA256(secret, body))
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
	if !got[0].Authenticated {
		t.Errorf("Authenticated = false, want true (signature matched armed binding's secret)")
	}
}

// TestHandleCaptureMarksUnauthenticatedWhenHMACMismatches covers the
// negative side of the HMAC verification: a body signed with the WRONG
// secret is still captured (per t2db.5 point 1 -- captures must remain
// inspectable even when auth fails) but flagged as unauthenticated so
// the dispatch loop's auth gate can drop it.
func TestHandleCaptureMarksUnauthenticatedWhenHMACMismatches(t *testing.T) {
	const secret = "abcdefghijklmnop"
	srv := captureBindingServer(t, secret, "github")
	body := `{"action":"opened"}`
	wrong := hmacSHA256("not-the-real-secret-aaaaaaaaa", body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", wrong)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (a mismatched signature must still produce a capture, not reject the request)", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("captured rows = %d, want 1", len(got))
	}
	if got[0].Authenticated {
		t.Errorf("Authenticated = true, want false (signature did not match armed binding's secret)")
	}
}

// TestHandleCaptureWithNoSignatureMarksUnauthenticated covers the
// "sender forgot the header" case: the binding is armed, the secret is
// registered, but the request carries no signature at all. The handler
// must fall through to authenticated=false (the empty-signature guard
// in webhookguard.VerifyHMAC). Captures are still recorded -- the
// operator can see the unsigned attempt in the inspector.
func TestHandleCaptureWithNoSignatureMarksUnauthenticated(t *testing.T) {
	const secret = "abcdefghijklmnop"
	srv := captureBindingServer(t, secret, "github")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	// Deliberately no X-Hub-Signature-256 / X-Signature-256 header.
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || got[0].Authenticated {
		t.Fatalf("captured = %+v, want 1 row with Authenticated=false", got)
	}
}

// TestHandleCaptureWithMultipleArmedBindingsReturns409 covers the
// belt-and-braces TOCTOU guard: two armed bindings for the same source
// would race over every inbound webhook for that source, so the
// capture-time handler returns 409 Conflict instead of silently picking
// a winner. The store's overlap check should make this impossible in
// practice; the 409 is the safety net. The stub injects the state
// directly to exercise that safety net.
func TestHandleCaptureWithMultipleArmedBindingsReturns409(t *testing.T) {
	srv := captureTestServer(t)
	srv.BindingDispatcher = &stubBindingStore{
		armedBySource: map[string][]binding.Binding{
			"github": {
				{ID: 1, Matcher: binding.Matcher{Source: "github"}, Status: binding.StatusArmed, Secret: "abcdefghijklmnop"},
				{ID: 2, Matcher: binding.Matcher{Source: "github"}, Status: binding.StatusArmed, Secret: "qrstuvwxyz123456"},
			},
		},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (multiple armed bindings must surface as 409 at capture time)", w.Code, http.StatusConflict)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("captured rows = %d, want 0 (a 409 must never persist a row)", len(got))
	}
}

// TestHandleCaptureAcceptsWhenNoArmedBindingExists documents the
// "source has never been bound" path: Bindings is wired but
// ArmedBindingsForSource returns an empty slice. The handler must
// still capture, with authenticated=false -- there is no armed binding
// to check against, so HMAC is meaningless, but the event itself is
// still worth recording for the inspector.
func TestHandleCaptureAcceptsWhenNoArmedBindingExists(t *testing.T) {
	srv := captureTestServer(t)
	srv.Bindings = &stubBindingStore{armedBySource: map[string][]binding.Binding{}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || got[0].Authenticated {
		t.Fatalf("captured = %+v, want 1 row with Authenticated=false", got)
	}
}

// TestHandleCaptureAcceptsWithBindingsNil pins the legacy backwards-
// compatibility behaviour: Bindings is nil (composition never wired the
// binding surface, or the dashboard is running without the feature).
// handleCapture must behave as it did before this phase landed -- still
// capture, still record authenticated=false.
func TestHandleCaptureAcceptsWithBindingsNil(t *testing.T) {
	srv := captureTestServer(t)
	if srv.Bindings != nil {
		t.Fatalf("captureTestServer must not wire Bindings by default for this test")
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || got[0].Authenticated {
		t.Fatalf("captured = %+v, want 1 row with Authenticated=false", got)
	}
}

// TestHandleCaptureBindingsLookupErrorFailsOpenAtCapture pins the
// documented fail-open posture: if ArmedBindingsForSource errors, the
// capture still goes through (the dispatch loop's auth check is the
// actual gate). A transient store hiccup must not amplify into a
// capture outage for sends-that-haven't-been-bound-yet.
func TestHandleCaptureBindingsLookupErrorFailsOpenAtCapture(t *testing.T) {
	srv := captureTestServer(t)
	srv.Bindings = &stubBindingStore{lookupErr: errors.New("db locked")}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (a transient lookup error must not fail closed at capture)", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || got[0].Authenticated {
		t.Fatalf("captured = %+v, want 1 row with Authenticated=false", got)
	}
}

// TestHandleCaptureAcceptsXSignature256FallbackHeader pins the second
// header in the precedence list: when X-Hub-Signature-256 is absent but
// X-Signature-256 is present, the fallback is honoured. Mirrors
// channels/webhook/webhook.go's header precedence.
func TestHandleCaptureAcceptsXSignature256FallbackHeader(t *testing.T) {
	const secret = "abcdefghijklmnop"
	srv := captureBindingServer(t, secret, "github")
	body := `{"action":"opened"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/capture/github", strings.NewReader(body))
	// No X-Hub-Signature-256; signature is in X-Signature-256 only.
	req.Header.Set("X-Signature-256", hmacSHA256(secret, body))
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	got, err := srv.Captures.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 || !got[0].Authenticated {
		t.Fatalf("captured = %+v, want 1 row with Authenticated=true (X-Signature-256 fallback must work)", got)
	}
}
