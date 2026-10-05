// Package captureintake receives and stores inbound webhooks for inspection
// and binding dispatch.
package captureintake

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
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
	Captures storecontract.CaptureStore
	// Limiter is the per-remote-address token bucket applied before a body
	// is read. Nil disables rate limiting, which composition never does in
	// production.
	Limiter *webhookguard.RateLimiter
	// Sources resolves the source a path names and its signing setting.
	// Nil disables verification: every event records as neither
	// authenticated nor unsigned, so none dispatches.
	Sources SourceResolver
	// Retention and MaxEvents are passed straight to InsertCapture's
	// prune-on-write bounds. See config.CaptureConfig.
	Retention time.Duration
	MaxEvents int
	// MaxBodyBytes rejects (413) a body larger than this before redaction
	// or storage sees it.
	MaxBodyBytes int64
	// Publish puts the capture's arrival on the activity stream, with the
	// request's context: the announce happens synchronously inside the
	// handler, before the 202 is written, so the request's lifetime bounds
	// it. Nil records the capture without announcing it.
	Publish func(ctx context.Context, e events.Event)
	// Delivery applies the network rules and the source's policies to the
	// sender's address. Nil admits every sender.
	Delivery access.DeliveryAuthorizer
	// Refusals counts what Delivery refused. Nil refuses without counting.
	Refusals storecontract.CaptureRefusalStore
	Log      *slog.Logger
}

// SourceResolver is the one SourceStore read intake needs.
type SourceResolver interface {
	GetSource(ctx context.Context, path string) (*source.Source, error)
}

// Register mounts the receiver on mux.
func (rc *Receiver) Register(mux *http.ServeMux) {
	mux.HandleFunc(Path, rc.ServeHTTP)
}

// ServeHTTP stores an inbound webhook. A valid HMAC on a signed source marks
// it authenticated; an approved unsigned source marks it unsigned; other
// events are stored but never dispatch.
func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rc.Captures == nil {
		http.Error(w, "capture not configured", http.StatusServiceUnavailable)
		return
	}
	path := r.PathValue("source")

	// Rate limit by remote address; the source segment is attacker-chosen.
	if rc.Limiter != nil && !rc.Limiter.Allow(remoteAddrHost(r)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	if rc.refused(r, path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	src := rc.resolveSource(r, path)

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytesOrFallback(rc.MaxBodyBytes))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// The only realistic cause here is MaxBytesReader tripping; any other
		// read failure also means there is no usable body to capture.
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Verified on the raw bytes, before redaction parses them.
	authenticated, unsigned := verify(src, r.Header, body)

	headers, _ := json.Marshal(r.Header)
	redactedHeaders, err := webhookguard.RedactPayload(headers)
	if err != nil {
		redactedHeaders = headers
	}
	// A body that isn't JSON has no key-value structure for the heuristic to
	// match against, so there is nothing to redact -- store it as received
	// rather than dropping or mangling it. Best-effort.
	redactedBody, err := webhookguard.RedactPayload(body)
	if err != nil {
		redactedBody = body
	}

	c := storecontract.CapturedEvent{
		ReceivedAt:    time.Now().UTC(),
		Source:        path,
		RemoteAddr:    r.RemoteAddr,
		ContentType:   r.Header.Get("Content-Type"),
		Headers:       string(redactedHeaders),
		Body:          string(redactedBody),
		Authenticated: authenticated,
		Unsigned:      unsigned,
	}
	id, err := rc.Captures.InsertCapture(r.Context(), c, rc.Retention, rc.MaxEvents)
	if err != nil {
		rc.logger().Error("capture insert", "err", err, "source", path)
		http.Error(w, "capture failed", http.StatusInternalServerError)
		return
	}
	// Reuses the existing activity-event pipeline rather than inventing
	// separate push plumbing. The captures view treats this purely as an
	// invalidation signal and refetches the list for the actual row data.
	if rc.Publish != nil {
		rc.Publish(r.Context(), events.Event{
			Kind:   "capture",
			Detail: "capture from " + path,
			Data:   map[string]any{"id": id, "source": path, "unsigned": unsigned},
		})
	}

	w.WriteHeader(http.StatusAccepted)
}

// refused applies the network rules before the body is read. A refused event
// is not stored, only counted on its source with the sender's address.
func (rc *Receiver) refused(r *http.Request, path string) bool {
	if rc.Delivery == nil {
		return false
	}
	addr := remoteAddrHost(r)
	if rc.Delivery.AuthorizeDelivery(org.DefaultOrgID, path, addr).Allowed {
		return false
	}
	if rc.Refusals != nil {
		if err := rc.Refusals.RecordCaptureRefusal(r.Context(), path, addr, time.Now()); err != nil {
			rc.logger().Warn("capture refusal not counted", "source", path, "err", err)
		}
	}
	return true
}

// resolveSource looks up the source a path names. A lookup error is logged
// and treated as an unknown source: the event is still captured but cannot
// dispatch, so failing open here costs no safety, while failing closed would
// turn a transient store hiccup into a capture outage.
func (rc *Receiver) resolveSource(r *http.Request, path string) *source.Source {
	if rc.Sources == nil {
		return nil
	}
	src, err := rc.Sources.GetSource(r.Context(), path)
	if err != nil {
		rc.logger().Warn("source lookup", "source", path, "err", err)
		return nil
	}
	return src
}

// verify reports how an event on src may dispatch: unsigned for an approved
// unsigned source, else authenticated by a valid X-Hub-Signature-256 or
// X-Signature-256. A source with no secret authenticates nothing.
func verify(src *source.Source, h http.Header, body []byte) (authenticated, unsigned bool) {
	switch {
	case src == nil:
		return false, false
	case src.Unsigned():
		return false, true
	case src.Secret == "":
		return false, false
	}
	sig := h.Get("X-Hub-Signature-256")
	if sig == "" {
		sig = h.Get("X-Signature-256")
	}
	return webhookguard.VerifyHMAC(body, sig, src.Secret), false
}

func (rc *Receiver) logger() *slog.Logger {
	if rc.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return rc.Log
}

// fallbackMaxBodyBytes bounds a capture body when MaxBodyBytes is left unset
// (zero).
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
