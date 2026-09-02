package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// defaultCapturesListLimit is used when the limit query param is absent,
// non-numeric, or non-positive. A raw strconv.Atoi zero value (its error
// case) must never reach ListCaptures directly -- SQL "LIMIT 0" returns zero
// rows, which would make an unrecognised or missing limit look identical to
// "no captures exist" instead of "show the default page."
const defaultCapturesListLimit = 100

// handleCapture accepts an inbound webhook POST and persists it. If a
// binding is armed for this source the body must also carry a valid HMAC
// signature; only correctly-signed events are recorded as authenticated
// (t2db.5 point 1). An unauthenticated event is still captured, visible in
// the inspector, and marked unauthenticated -- the dispatch loop's auth
// check (binding.Matcher.Matches) is the actual gate against false task
// creation. Mounted on the bypass mux alongside /healthz: capture must
// accept unauthenticated senders, so it cannot sit behind requireToken.
func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if s.Captures == nil {
		http.Error(w, "capture not configured", http.StatusServiceUnavailable)
		return
	}
	source := r.PathValue("source")

	// webhookguard.RateLimiter's bucket map assumes a bounded, operator-
	// registered key space (see its doc comment) -- it is never evicted, and
	// a fresh key starts with a full burst allowance. source is an
	// unregistered, attacker-chosen URL segment by design (this endpoint
	// exists to capture senders archie has no registration for yet), so
	// keying on it would let a single attacker rotate a new "source" on
	// every request to bypass the limit entirely and grow the bucket map
	// without bound. Remote address is the bounded identity available here.
	if s.CaptureLimiter != nil && !s.CaptureLimiter.Allow(remoteAddrHost(r)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Resolve armed bindings for this source before reading the body. A
	// lookup error is logged and treated as "no armed binding" -- the
	// dispatch loop's auth check is the actual gate, so failing closed here
	// would amplify a transient store hiccup into a capture outage for
	// every senders-that-haven't-been-bound-yet flow. If a future phase
	// wants fail-closed semantics, that's a separate decision and should
	// not silently land here.
	var armed []binding.Binding
	if s.BindingDispatcher != nil {
		var err error
		armed, err = s.BindingDispatcher.ArmedBindingsForSource(r.Context(), source)
		if err != nil {
			s.Log.Warn("armed bindings lookup", "source", source, "err", err)
			armed = nil
		}
	}
	// Two armed bindings for one source would race over every inbound
	// webhook for that source: the write-time overlap check should make
	// this state impossible, but the dispatch loop needs a single binding
	// per capture so we surface the overlap as 409 at capture time
	// (belt-and-braces TOCTOU guard -- see bindings.go's ApproveBinding).
	if len(armed) > 1 {
		http.Error(w,
			fmt.Sprintf("multiple armed bindings for source %q -- overlap rejected", source),
			http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, captureMaxBodyBytesOrFallback(s.CaptureMaxBodyBytes))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// The only realistic cause here is MaxBytesReader tripping; any other
		// read failure also means there is no usable body to capture.
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// HMAC verification -- only when exactly one binding is armed for this
	// source. Mirrors channels/webhook/webhook.go's header precedence:
	// GitHub-style X-Hub-Signature-256 first, X-Signature-256 fallback.
	// Empty header + non-empty secret is never valid (VerifyHMAC's own
	// guard), so an absent signature with an armed binding lands here as
	// authenticated=false rather than authenticated=true.
	authenticated := false
	if len(armed) == 1 {
		sig := r.Header.Get("X-Hub-Signature-256")
		if sig == "" {
			sig = r.Header.Get("X-Signature-256")
		}
		authenticated = webhookguard.VerifyHMAC(body, sig, armed[0].Secret)
	}

	headers, _ := json.Marshal(r.Header)
	redactedHeaders, err := webhookguard.RedactPayload(headers)
	if err != nil {
		redactedHeaders = headers
	}
	// A body that isn't JSON has no key-value structure for the heuristic to
	// match against, so there is nothing to redact -- store it as received
	// rather than dropping or mangling it. Best-effort, per
	// docs/prds/webhook-intake-security.md point 5.
	redactedBody, err := webhookguard.RedactPayload(body)
	if err != nil {
		redactedBody = body
	}

	c := store.CapturedEvent{
		ReceivedAt:    time.Now().UTC(),
		Source:        source,
		RemoteAddr:    r.RemoteAddr,
		ContentType:   r.Header.Get("Content-Type"),
		Headers:       string(redactedHeaders),
		Body:          string(redactedBody),
		Authenticated: authenticated,
	}
	id, err := s.Captures.InsertCapture(r.Context(), c, s.CaptureRetention, s.CaptureMaxEvents)
	if err != nil {
		s.Log.Error("capture insert", "err", err, "source", source)
		http.Error(w, "capture failed", http.StatusInternalServerError)
		return
	}
	// Reuses the existing operator-activity event pipeline (persist + live
	// SSE fan-out) rather than inventing separate push plumbing -- see
	// docs/prds/event-capture-storage.md. Deliberately LIGHTWEIGHT: the
	// events table this feeds (internal/store/events.go) has no retention or
	// row-count prune, unlike captured_events, so embedding the full (up to
	// CaptureMaxBodyBytes) body/headers here would duplicate the payload
	// into an unbounded table and defeat InsertCapture's own disk-bound
	// guarantee. The dedicated captures view (t2db.2) treats this purely as
	// an invalidation signal and refetches GET /api/captures for the actual
	// row data, rather than merging this event's fields into its list.
	s.emit(r.Context(), events.Event{
		Kind:   "capture",
		Detail: "capture from " + source,
		Data:   map[string]any{"id": id, "source": source},
	})

	w.WriteHeader(http.StatusAccepted)
}

// handleCaptures lists recent captured events, newest first, for the
// dashboard's event inspector (t2db.2). Token-gated like every other
// /api/* route -- captured payloads are visible only to an authenticated
// operator, per docs/prds/webhook-intake-security.md point 5.
func (s *Server) handleCaptures(w http.ResponseWriter, r *http.Request) {
	if s.Captures == nil {
		writeJSON(w, map[string]any{"captures": []any{}, "enabled": false})
		return
	}

	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultCapturesListLimit
	}
	captures, err := s.Captures.ListCaptures(r.Context(), limit)
	if err != nil {
		s.Log.Error("list captures", "err", err)
		http.Error(w, "list captures failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"captures": captures, "enabled": true})
}

// fallbackCaptureMaxBodyBytes bounds a capture body when CaptureMaxBodyBytes
// is left unset (zero). The size cap is the only one of the three disk-bound
// mechanics in docs/prds/event-capture-storage.md that limits a SINGLE
// payload rather than total row count or write rate -- a table pruned to
// maxEvents rows still holds one arbitrarily large row if nothing ever
// bounded that row's size, so this must never be skippable by a missing
// wiring value the way the rate limiter (best-effort, not disk-bound-load-
// bearing on its own) is allowed to be.
const fallbackCaptureMaxBodyBytes = 256 * 1024

func captureMaxBodyBytesOrFallback(configured int64) int64 {
	if configured > 0 {
		return configured
	}
	return fallbackCaptureMaxBodyBytes
}

// remoteAddrHost strips the port from r.RemoteAddr for use as a rate-limit
// key, falling back to the raw value if it isn't a host:port pair.
func remoteAddrHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
