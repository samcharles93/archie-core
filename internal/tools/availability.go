package tools

import (
	"sync"
	"time"
)

// AvailabilityCache wraps a [CheckFn] with TTL-based result caching.
//
// Positive results (tool is available) are cached for the TTL duration.
// Negative results (tool is unavailable) are cached for the failure
// grace period. A nil check function means "always available"  --  Check
// returns true immediately with no caching overhead.
//
// All methods are safe for concurrent use.
type AvailabilityCache struct {
	TTL                time.Duration
	FailureGracePeriod time.Duration

	check CheckFn

	mu         sync.Mutex
	lastResult bool
	checkedAt  time.Time
}

// NewAvailabilityCache creates an [AvailabilityCache] wrapping the given
// check function. If check is nil, Check always returns true (the tool
// is permanently available and no caching is needed).
//
// Typical values: TTL=30s for positive results, FailureGracePeriod=60s
// for negative results (avoid churn when a dependency is temporarily down).
func NewAvailabilityCache(check CheckFn, ttl, failureGrace time.Duration) *AvailabilityCache {
	return &AvailabilityCache{
		TTL:                ttl,
		FailureGracePeriod: failureGrace,
		check:              check,
	}
}

// Check reports whether the tool is available. It returns the cached
// result when the cache is still fresh; otherwise it re-evaluates the
// underlying check function.
//
// When the underlying check returns true (available), the result is
// cached for c.TTL. When it returns false (unavailable), the result is
// cached for c.FailureGracePeriod  --  this prevents tight re-check loops
// while a dependency is temporarily down.
func (c *AvailabilityCache) Check() bool {
	if c.check == nil {
		return true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cacheDuration := c.TTL
	if !c.lastResult {
		cacheDuration = c.FailureGracePeriod
	}

	if c.checkedAt.IsZero() || now.Sub(c.checkedAt) >= cacheDuration {
		c.lastResult = c.check()
		c.checkedAt = now
	}

	return c.lastResult
}

// Invalidate forces the next call to Check to bypass the cache and
// re-evaluate the underlying check function.
func (c *AvailabilityCache) Invalidate() {
	c.mu.Lock()
	c.checkedAt = time.Time{}
	c.mu.Unlock()
}

// LastChecked reports when the cache was last refreshed. A zero time
// means the cache has never been evaluated.
func (c *AvailabilityCache) LastChecked() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkedAt
}

// CachedAvailable returns the most recent cached result without
// re-evaluating the check function. For tools with a nil check,
// this always returns true.
func (c *AvailabilityCache) CachedAvailable() bool {
	if c.check == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastResult
}

// NewAvailabilityCacheFromEntry is a convenience constructor that creates
// an [AvailabilityCache] from a [ToolEntry]'s CheckFn using the given TTL
// and failure grace period. When the entry has a nil CheckFn the returned
// cache reports always-available.
func NewAvailabilityCacheFromEntry(e ToolEntry, ttl, failureGrace time.Duration) *AvailabilityCache {
	return NewAvailabilityCache(e.CheckFn, ttl, failureGrace)
}
