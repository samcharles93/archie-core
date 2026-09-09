// Package captureintake is the HTTP receiver for inbound webhooks archie has
// no binding for yet: it persists what arrives so an operator can inspect it
// and build one (docs/prds/event-capture-storage.md).
//
// It belongs to the process that owns work intake, not to the dashboard that
// displays captures. It served from the dashboard's listener while the two
// shared a process; the split gives the read to the dashboard, over the State
// Store, and keeps the write here (archie-core-8cda.5.4).
package captureintake

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// Path is the route this receiver serves. It is unauthenticated by design:
// the senders it exists to capture have no credential here yet.
const Path = "POST /webhooks/capture/{source}"

// Receiver persists inbound webhook captures.
type Receiver struct {
	// Captures persists what arrives. Nil answers 503 rather than failing
	// the process it is mounted in: capture is a precondition for the
	// no-code playbook epic, not a hard dependency of the daemon.
	Captures store.CaptureStore
	// Limiter is the per-remote-address token bucket applied before a body
	// is read. Nil disables rate limiting, which composition never does in
	// production.
	Limiter *webhookguard.RateLimiter
	// Bindings resolves the armed binding whose secret authenticates a
	// source. Nil disables per-source HMAC verification and every event
	// records as authenticated=false.
	Bindings store.BindingDispatcher
	// Retention and MaxEvents are passed straight to InsertCapture's
	// prune-on-write bounds. See config.CaptureConfig.
	Retention time.Duration
	MaxEvents int
	// MaxBodyBytes rejects (413) a body larger than this before redaction
	// or storage sees it.
	MaxBodyBytes int64
	// Publish puts the capture's arrival on the activity stream. Nil records
	// the capture without announcing it.
	Publish func(events.Event)
	Log     *slog.Logger
}

// Register mounts the receiver on mux.
func (rc *Receiver) Register(mux *http.ServeMux) {
	mux.HandleFunc(Path, rc.ServeHTTP)
}

// ServeHTTP accepts an inbound webhook POST and persists it. If a binding is
// armed for this source the body must also carry a valid HMAC signature; only
// correctly-signed events are recorded as authenticated (t2db.5 point 1). An
// unauthenticated event is still captured, visible in the inspector, and
// marked unauthenticated -- the dispatch loop's auth check
// (binding.Matcher.Matches) is the actual gate against false task creation.
func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rc.Captures == nil {
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
	if rc.Limiter != nil && !rc.Limiter.Allow(remoteAddrHost(r)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	armed, ok := rc.armedBindings(w, r, source)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytesOrFallback(rc.MaxBodyBytes))
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
	id, err := rc.Captures.InsertCapture(r.Context(), c, rc.Retention, rc.MaxEvents)
	if err != nil {
		rc.logger().Error("capture insert", "err", err, "source", source)
		http.Error(w, "capture failed", http.StatusInternalServerError)
		return
	}
	// Reuses the existing activity-event pipeline rather than inventing
	// separate push plumbing -- see docs/prds/event-capture-storage.md.
	// Deliberately LIGHTWEIGHT: the events table this feeds has no retention
	// or row-count prune, unlike captured_events, so embedding the full (up
	// to MaxBodyBytes) body/headers here would duplicate the payload into an
	// unbounded table and defeat InsertCapture's own disk-bound guarantee.
	// The captures view treats this purely as an invalidation signal and
	// refetches the list for the actual row data.
	if rc.Publish != nil {
		rc.Publish(events.Event{
			Kind:   "capture",
			Detail: "capture from " + source,
			Data:   map[string]any{"id": id, "source": source},
		})
	}

	w.WriteHeader(http.StatusAccepted)
}

// armedBindings resolves the bindings armed for source before the body is
// read. A lookup error is logged and treated as "no armed binding" -- the
// dispatch loop's auth check is the actual gate, so failing closed here would
// amplify a transient store hiccup into a capture outage for every
// sender-not-yet-bound flow. If a future phase wants fail-closed semantics,
// that is a separate decision and should not silently land here.
//
// Two armed bindings for one source would race over every inbound webhook for
// that source: the write-time overlap check should make this state
// impossible, but the dispatch loop needs a single binding per capture, so
// the overlap surfaces as 409 at capture time (belt-and-braces TOCTOU guard
// -- see the store's ApproveBinding).
func (rc *Receiver) armedBindings(w http.ResponseWriter, r *http.Request, source string) ([]binding.Binding, bool) {
	if rc.Bindings == nil {
		return nil, true
	}
	armed, err := rc.Bindings.ArmedBindingsForSource(r.Context(), source)
	if err != nil {
		rc.logger().Warn("armed bindings lookup", "source", source, "err", err)
		return nil, true
	}
	if len(armed) > 1 {
		http.Error(w,
			fmt.Sprintf("multiple armed bindings for source %q -- overlap rejected", source),
			http.StatusConflict)
		return nil, false
	}
	return armed, true
}

func (rc *Receiver) logger() *slog.Logger {
	if rc.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return rc.Log
}

// fallbackMaxBodyBytes bounds a capture body when MaxBodyBytes is left unset
// (zero). The size cap is the only one of the three disk-bound mechanics in
// docs/prds/event-capture-storage.md that limits a SINGLE payload rather than
// total row count or write rate -- a table pruned to maxEvents rows still
// holds one arbitrarily large row if nothing ever bounded that row's size, so
// this must never be skippable by a missing wiring value the way the rate
// limiter (best-effort, not disk-bound-load-bearing on its own) is allowed to
// be.
const fallbackMaxBodyBytes = 256 * 1024

func maxBodyBytesOrFallback(configured int64) int64 {
	if configured > 0 {
		return configured
	}
	return fallbackMaxBodyBytes
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
