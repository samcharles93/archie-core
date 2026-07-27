package ratelimit

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestLimiter(window time.Duration, maxRequests int) (*Limiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(window, maxRequests)
	l.now = clock.Now
	return l, clock
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLimiterAllowsUpToMax(t *testing.T) {
	tests := []struct {
		name string
		max  int
	}{
		{"max one", 1},
		{"max three", 3},
		{"max ten", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, _ := newTestLimiter(time.Minute, tt.max)
			for i := range tt.max {
				if !l.Allow("telegram", "user1") {
					t.Fatalf("request %d: Allow() = false, want true (max %d)", i, tt.max)
				}
			}
		})
	}
}

func TestLimiterBlocksOverMax(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 3)
	for i := range 3 {
		if !l.Allow("telegram", "user1") {
			t.Fatalf("request %d: Allow() = false, want true", i)
		}
	}
	if l.Allow("telegram", "user1") {
		t.Error("4th request within window: Allow() = true, want false")
	}
}

func TestLimiterWindowSlides(t *testing.T) {
	l, clock := newTestLimiter(time.Minute, 2)
	if !l.Allow("telegram", "user1") {
		t.Fatal("expected 1st request to be allowed")
	}
	if !l.Allow("telegram", "user1") {
		t.Fatal("expected 2nd request to be allowed")
	}
	if l.Allow("telegram", "user1") {
		t.Fatal("3rd request should be blocked")
	}

	clock.Advance(time.Minute + time.Second)
	if !l.Allow("telegram", "user1") {
		t.Error("request after window elapsed: Allow() = false, want true")
	}
}

func TestLimiterPartialWindowSlide(t *testing.T) {
	l, clock := newTestLimiter(time.Minute, 2)
	if !l.Allow("telegram", "user1") {
		t.Fatal("1st request should be allowed")
	}
	clock.Advance(31 * time.Second)
	if !l.Allow("telegram", "user1") {
		t.Fatal("2nd request should be allowed")
	}
	if l.Allow("telegram", "user1") {
		t.Fatal("3rd request should be blocked, both prior hits still in window")
	}

	// Advance so only the 1st hit falls out of the window; the 2nd is
	// still within it, so this must free exactly one slot.
	clock.Advance(30 * time.Second)
	if !l.Allow("telegram", "user1") {
		t.Error("expected one freed slot after the 1st hit aged out")
	}
	if l.Allow("telegram", "user1") {
		t.Error("expected no further slots; 2nd and 4th hits still in window")
	}
}

func TestLimiterPerUserIndependent(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)
	if !l.Allow("telegram", "user1") {
		t.Fatal("user1 1st request should be allowed")
	}
	if !l.Allow("telegram", "user2") {
		t.Error("user2 must not be blocked by user1's usage")
	}
	if l.Allow("telegram", "user1") {
		t.Error("user1 2nd request should be blocked")
	}
}

func TestLimiterPerPlatformIndependent(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)
	if !l.Allow("telegram", "user1") {
		t.Fatal("telegram user1 1st request should be allowed")
	}
	if !l.Allow("discord", "user1") {
		t.Error("same userID on a different platform must not be blocked")
	}
}

func TestLimiterZeroMaxAlwaysBlocks(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 0)
	if l.Allow("telegram", "user1") {
		t.Error("max=0 should block every request")
	}
}

func TestLimiterNegativeMaxAlwaysBlocks(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, -1)
	if l.Allow("telegram", "user1") {
		t.Error("max=-1 should block every request")
	}
}

func TestLimiterEmptyPlatformAndUserIDIsAValidKey(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)
	if !l.Allow("", "") {
		t.Fatal("empty platform/userID should still get its own budget")
	}
	if l.Allow("", "") {
		t.Error("2nd request with empty platform/userID should be blocked")
	}
	if !l.Allow("telegram", "user1") {
		t.Error("a real (platform, userID) pair must not be affected by the empty-string one")
	}
}

func TestLimiterNoCrossTalkAcrossConcatenationBoundary(t *testing.T) {
	// A naive "platform + separator + userID" string key would collide
	// here if the separator byte ever appeared inside a caller-supplied
	// field. Regression test for that class of bug: these two distinct
	// (platform, userID) pairs must never share a budget.
	l, _ := newTestLimiter(time.Minute, 1)
	if !l.Allow("a\x00b", "c") {
		t.Fatal("1st pair's 1st request should be allowed")
	}
	if !l.Allow("a", "b\x00c") {
		t.Error("a differently-split pair that would concatenate to the same string must have its own independent budget")
	}
}

func TestLimiterConcurrentAccessRespectsMax(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 5)

	const n = 20
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for range n {
		wg.Go(func() {
			results <- l.Allow("telegram", "user1")
		})
	}
	wg.Wait()
	close(results)

	allowed := 0
	for r := range results {
		if r {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("allowed = %d, want 5", allowed)
	}
}

func TestLimiterConcurrentDifferentKeysNoCrossTalk(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)

	const n = 20
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := range n {
		wg.Go(func() {
			results <- l.Allow("telegram", fmt.Sprintf("user%d", i))
		})
	}
	wg.Wait()
	close(results)

	allowed := 0
	for r := range results {
		if r {
			allowed++
		}
	}
	if allowed != n {
		t.Errorf("allowed = %d, want %d (each user has an independent budget)", allowed, n)
	}
}
