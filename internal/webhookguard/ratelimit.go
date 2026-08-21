package webhookguard

import (
	"sync"
	"time"
)

// RateLimiter caps request volume per source using a token bucket per
// source ID. Source IDs are assumed to come from a registered, operator-
// controlled set (per docs/prds/webhook-intake-security.md's "a source
// registers with a secret before its events can trigger anything"), not
// from unbounded attacker-supplied request data -- the bucket map is not
// itself bounded, because the key space is bounded by registration.
//
// A rejected request must be surfaced as an observable rejection by the
// caller (HTTP 429), never silently dropped -- see
// docs/prds/webhook-intake-security.md point 3.
type RateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens added per second
	burst   float64 // bucket capacity, also the starting balance
	now     func() time.Time
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewRateLimiter returns a limiter refilling at ratePerSecond tokens per
// second up to burst tokens. now is the clock to use; pass time.Now for
// production callers and an injected clock in tests.
func NewRateLimiter(ratePerSecond float64, burst int, now func() time.Time) *RateLimiter {
	return &RateLimiter{
		rate:    ratePerSecond,
		burst:   float64(burst),
		now:     now,
		buckets: make(map[string]*tokenBucket),
	}
}

// Allow reports whether a request from source may proceed, consuming one
// token if so. A source seen for the first time starts with a full bucket
// (burst tokens) rather than empty, so the initial request from a newly
// registered source is never rejected outright.
func (r *RateLimiter) Allow(source string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	b, ok := r.buckets[source]
	if !ok {
		b = &tokenBucket{tokens: r.burst, lastSeen: now}
		r.buckets[source] = b
	} else {
		elapsed := now.Sub(b.lastSeen).Seconds()
		if elapsed > 0 {
			b.tokens = min(r.burst, b.tokens+elapsed*r.rate)
			b.lastSeen = now
		}
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
