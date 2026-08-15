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

func TestEvictStaleRemovesIdleEntries(t *testing.T) {
	l, clock := newTestLimiter(time.Minute, 3)
	if !l.Allow("telegram", "user1") {
		t.Fatal("1st request should be allowed")
	}

	// The single hit is now two windows old; a sweep must drop the entry.
	clock.Advance(2 * time.Minute)
	if got := l.EvictStale(); got != 1 {
		t.Fatalf("EvictStale() = %d, want 1", got)
	}
	if len(l.hits) != 0 {
		t.Errorf("hits map has %d entries after sweep, want 0", len(l.hits))
	}
}

func TestEvictStaleKeepsRecentEntries(t *testing.T) {
	l, clock := newTestLimiter(time.Minute, 2)
	if !l.Allow("telegram", "user1") {
		t.Fatal("user1 1st request should be allowed")
	}
	clock.Advance(30 * time.Second)
	if !l.Allow("telegram", "user2") {
		t.Fatal("user2 1st request should be allowed")
	}

	// Sweep 80s in: user1's hit (t=0) has aged out; user2's (t=30s) is
	// still inside the one-minute window.
	clock.Advance(50 * time.Second)
	if got := l.EvictStale(); got != 1 {
		t.Fatalf("EvictStale() = %d, want 1", got)
	}
	if _, ok := l.hits[limiterKey{platform: "telegram", userID: "user1"}]; ok {
		t.Error("user1 entry should have been evicted")
	}
	if _, ok := l.hits[limiterKey{platform: "telegram", userID: "user2"}]; !ok {
		t.Error("user2 entry should have been kept")
	}
}

func TestEvictStaleBoundaryIsWindowCutoff(t *testing.T) {
	// EvictStale's cutoff is Allow's own pruning cutoff (now - window):
	// a hit exactly one window ago is stale, one nanosecond inside is not.
	tests := []struct {
		name    string
		advance time.Duration
		want    int
	}{
		{"hit well inside window is kept", 30 * time.Second, 0},
		{"hit one nanosecond inside window is kept", time.Minute - time.Nanosecond, 0},
		{"hit exactly one window ago is stale", time.Minute, 1},
		{"hit well past the window is stale", 2 * time.Minute, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, clock := newTestLimiter(time.Minute, 2)
			if !l.Allow("telegram", "user1") {
				t.Fatal("1st request should be allowed")
			}
			clock.Advance(tt.advance)
			if got := l.EvictStale(); got != tt.want {
				t.Errorf("EvictStale() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEvictStalePreservesAllowOutcome(t *testing.T) {
	// A sweep must never change what Allow would have decided: it only
	// removes keys whose hits would have been pruned anyway, so an
	// in-window key stays at its budget and an aged-out key gets a fresh
	// one either way.
	t.Run("in-window key stays blocked", func(t *testing.T) {
		l, clock := newTestLimiter(time.Minute, 1)
		if !l.Allow("telegram", "user1") {
			t.Fatal("1st request should be allowed")
		}
		clock.Advance(61 * time.Second)
		if !l.Allow("telegram", "user1") {
			t.Fatal("2nd request after the 1st hit aged out should be allowed")
		}
		clock.Advance(31 * time.Second) // t=92s; the t=61s hit is still in-window
		if got := l.EvictStale(); got != 0 {
			t.Fatalf("EvictStale() = %d, want 0 (hit still in window)", got)
		}
		if l.Allow("telegram", "user1") {
			t.Error("in-window key must remain blocked after a sweep")
		}
	})

	t.Run("aged-out key gets fresh budget either way", func(t *testing.T) {
		l, clock := newTestLimiter(time.Minute, 1)
		if !l.Allow("telegram", "user1") {
			t.Fatal("1st request should be allowed")
		}
		clock.Advance(2 * time.Minute)
		if got := l.EvictStale(); got != 1 {
			t.Fatalf("EvictStale() = %d, want 1 (hit aged out)", got)
		}
		if !l.Allow("telegram", "user1") {
			t.Error("aged-out key should get a fresh budget after the sweep")
		}
	})
}

func TestEvictStaleRemovesEmptySliceEntries(t *testing.T) {
	// max=0 stores an empty slice on every block; such an entry holds no
	// hits at all and must be removed by any sweep.
	l, _ := newTestLimiter(time.Minute, 0)
	if l.Allow("telegram", "user1") {
		t.Fatal("max=0 should block the request")
	}
	if got := l.EvictStale(); got != 1 {
		t.Fatalf("EvictStale() = %d, want 1 (empty-slice entry)", got)
	}
}

func TestEvictStaleRemovesNothingWhileHitsFresh(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)
	if !l.Allow("telegram", "user1") {
		t.Fatal("user1 1st request should be allowed")
	}
	if !l.Allow("telegram", "user2") {
		t.Fatal("user2 1st request should be allowed")
	}
	// All hits are within the window: a sweep removes nothing.
	if got := l.EvictStale(); got != 0 {
		t.Fatalf("EvictStale() = %d, want 0 (all hits fresh)", got)
	}
	if len(l.hits) != 2 {
		t.Errorf("hits map has %d entries, want 2", len(l.hits))
	}
}

func TestEvictStaleEmptyMap(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)
	if got := l.EvictStale(); got != 0 {
		t.Errorf("EvictStale() on an empty limiter = %d, want 0", got)
	}
}

func TestEvictStaleConcurrentWithAllow(t *testing.T) {
	l, clock := newTestLimiter(time.Minute, 1000)

	const staleKeys = 20
	for i := range staleKeys {
		if !l.Allow("telegram", fmt.Sprintf("stale%d", i)) {
			t.Fatalf("seed request for stale%d should be allowed", i)
		}
	}
	// Age the seed hits out of the window so the sweep actually has entries
	// to delete while Allow races to keep a separate key fresh (the suite
	// runs with -race). The clock stays frozen for the rest of the test, so
	// the stale keys can never become live again and a correct EvictStale
	// must remove all of them by the time the race settles.
	clock.Advance(2 * time.Minute)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { _ = l.Allow("telegram", "fresh") })
		wg.Go(func() { _ = l.EvictStale() })
	}
	wg.Wait()

	if _, ok := l.hits[limiterKey{platform: "telegram", userID: "fresh"}]; !ok {
		t.Error("fresh key evicted despite concurrent Allow keeping it live")
	}
	if len(l.hits) != 1 {
		t.Errorf("hits map has %d entries, want 1 (only the fresh key); stale entries were not evicted", len(l.hits))
	}
}
