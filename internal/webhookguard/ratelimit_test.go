package webhookguard_test

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// fakeClock lets tests advance time deterministically instead of sleeping.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func TestRateLimiterAllowsUpToBurstThenDenies(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := webhookguard.NewRateLimiter(1, 3, clock.Now) // 1/s refill, burst 3

	for i := range 3 {
		if !limiter.Allow("source-a") {
			t.Fatalf("request %d: Allow() = false, want true (within burst)", i)
		}
	}
	if limiter.Allow("source-a") {
		t.Fatal("Allow() = true, want false (burst exhausted)")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := webhookguard.NewRateLimiter(1, 1, clock.Now) // 1/s refill, burst 1

	if !limiter.Allow("source-a") {
		t.Fatal("first request denied, want allowed")
	}
	if limiter.Allow("source-a") {
		t.Fatal("second immediate request allowed, want denied")
	}
	clock.advance(time.Second)
	if !limiter.Allow("source-a") {
		t.Fatal("request after refill window denied, want allowed")
	}
}

func TestRateLimiterSourcesAreIndependent(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := webhookguard.NewRateLimiter(1, 1, clock.Now)

	if !limiter.Allow("source-a") {
		t.Fatal("source-a first request denied, want allowed")
	}
	if limiter.Allow("source-a") {
		t.Fatal("source-a burst exhausted request allowed, want denied")
	}
	if !limiter.Allow("source-b") {
		t.Fatal("source-b denied by source-a's exhausted budget, want independent")
	}
}

func TestRateLimiterUnknownSourceStartsWithFullBurst(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := webhookguard.NewRateLimiter(1, 5, clock.Now)

	for i := range 5 {
		if !limiter.Allow("first-time-source") {
			t.Fatalf("request %d for a never-seen source denied, want allowed within burst", i)
		}
	}
}
