package webui

import (
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// handleCapture accepts an unbound inbound webhook POST and persists it --
// no HMAC verification (there is by design no secret registered yet for a
// source nobody has configured), no workflow binding, just "something
// arrived." See docs/prds/event-capture-storage.md. Mounted on the bypass
// mux alongside /healthz: capture must accept unauthenticated senders, so it
// cannot sit behind requireToken.
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

	r.Body = http.MaxBytesReader(w, r.Body, captureMaxBodyBytesOrFallback(s.CaptureMaxBodyBytes))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// The only realistic cause here is MaxBytesReader tripping; any other
		// read failure also means there is no usable body to capture.
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
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

	_, err = s.Captures.InsertCapture(r.Context(), store.CapturedEvent{
		Source:      source,
		RemoteAddr:  r.RemoteAddr,
		ContentType: r.Header.Get("Content-Type"),
		Headers:     string(redactedHeaders),
		Body:        string(redactedBody),
	}, s.CaptureRetention, s.CaptureMaxEvents)
	if err != nil {
		s.Log.Error("capture insert", "err", err, "source", source)
		http.Error(w, "capture failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
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
