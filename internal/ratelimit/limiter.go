// Package ratelimit provides a sliding-window request limiter, keyed by
// platform and user, for gating inbound gateway traffic per identity
// rather than globally.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter enforces "at most max requests per window" per (platform,
// userID) pair using a sliding window log. It is safe for concurrent
// use.
type Limiter struct {
	window time.Duration
	max    int
	now    func() time.Time

	mu   sync.Mutex
	hits map[limiterKey][]time.Time
}

// limiterKey identifies one (platform, userID) budget. Using a struct
// rather than a concatenated string avoids any risk of two distinct
// pairs colliding on a chosen separator byte that turns up in
// externally-supplied platform or userID values.
type limiterKey struct {
	platform string
	userID   string
}

// New returns a Limiter allowing at most max requests in any rolling
// window-length interval, per (platform, userID) pair. A max of 0 blocks
// every request.
func New(window time.Duration, max int) *Limiter {
	return &Limiter{
		window: window,
		max:    max,
		now:    time.Now,
		hits:   map[limiterKey][]time.Time{},
	}
}

// Allow reports whether a request from userID on platform is within the
// limit, recording it if so.
func (l *Limiter) Allow(platform, userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	key := limiterKey{platform: platform, userID: userID}
	cutoff := now.Add(-l.window)

	hits := l.hits[key]
	live := hits[:0]
	for _, h := range hits {
		if h.After(cutoff) {
			live = append(live, h)
		}
	}

	if len(live) >= l.max {
		l.hits[key] = live
		return false
	}

	l.hits[key] = append(live, now)
	return true
}
