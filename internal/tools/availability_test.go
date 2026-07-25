package tools

import (
	"testing"
	"time"
)

func TestAvailabilityCacheAlwaysAvailable(t *testing.T) {
	c := NewAvailabilityCache(nil, 30*time.Second, 60*time.Second)

	if !c.Check() {
		t.Error("nil CheckFn should mean always available")
	}
	if !c.CachedAvailable() {
		t.Error("nil CheckFn should mean always available")
	}
}

func TestAvailabilityCacheCheck(t *testing.T) {
	t.Run("positive result cached", func(t *testing.T) {
		calls := 0
		c := NewAvailabilityCache(func() bool {
			calls++
			return true
		}, 30*time.Second, 60*time.Second)

		// First call  --  evaluates.
		if !c.Check() {
			t.Error("expected true")
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}

		// Second call  --  cached.
		if !c.Check() {
			t.Error("expected true (cached)")
		}
		if calls != 1 {
			t.Errorf("expected still 1 call (cached), got %d", calls)
		}

		// CachedAvailable reports the last result.
		if !c.CachedAvailable() {
			t.Error("CachedAvailable should be true")
		}
	})

	t.Run("negative result cached with failure grace", func(t *testing.T) {
		calls := 0
		c := NewAvailabilityCache(func() bool {
			calls++
			return false
		}, 100*time.Millisecond, 300*time.Millisecond)

		// First call.
		if c.Check() {
			t.Error("expected false")
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}

		// Still within failure grace period  --  cached false.
		if c.Check() {
			t.Error("expected false (cached within grace)")
		}
		if calls != 1 {
			t.Errorf("expected still 1 call, got %d", calls)
		}
	})
}

func TestAvailabilityCacheExpiry(t *testing.T) {
	calls := 0
	c := NewAvailabilityCache(func() bool {
		calls++
		return true
	}, 20*time.Millisecond, 50*time.Millisecond)

	// First call.
	c.Check()
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Wait for TTL to expire.
	time.Sleep(30 * time.Millisecond)

	// TTL expired  --  re-evaluates.
	c.Check()
	if calls != 2 {
		t.Errorf("expected re-evaluation after TTL expiry, got %d calls", calls)
	}
}

func TestAvailabilityCacheInvalidate(t *testing.T) {
	calls := 0
	c := NewAvailabilityCache(func() bool {
		calls++
		return true
	}, 3600*time.Second, 3600*time.Second)

	// Fill cache.
	c.Check()
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Cached call.
	c.Check()
	if calls != 1 {
		t.Error("expected cache hit")
	}

	// Invalidate.
	c.Invalidate()

	// Re-evaluates.
	c.Check()
	if calls != 2 {
		t.Errorf("expected re-evaluation after invalidation, got %d calls", calls)
	}
}

func TestAvailabilityCacheLastChecked(t *testing.T) {
	c := NewAvailabilityCache(func() bool { return true }, 30*time.Second, 60*time.Second)

	if !c.LastChecked().IsZero() {
		t.Error("LastChecked should be zero before first Check()")
	}

	before := time.Now()
	c.Check()
	after := time.Now()

	lc := c.LastChecked()
	if lc.Before(before) || lc.After(after) {
		t.Errorf("LastChecked = %v, expected between %v and %v", lc, before, after)
	}
}

func TestNewAvailabilityCacheFromEntry(t *testing.T) {
	e := ToolEntry{
		Name:    "test-tool",
		Handler: noopHandler,
		CheckFn: func() bool { return true },
	}

	c := NewAvailabilityCacheFromEntry(e, 30*time.Second, 60*time.Second)
	if !c.Check() {
		t.Error("expected available")
	}

	// Nil CheckFn → always available.
	eNil := ToolEntry{Name: "always", Handler: noopHandler}
	cNil := NewAvailabilityCacheFromEntry(eNil, 30*time.Second, 60*time.Second)
	if !cNil.Check() {
		t.Error("nil CheckFn should mean always available")
	}
}

func TestAvailabilityCacheConcurrent(t *testing.T) {
	calls := 0
	c := NewAvailabilityCache(func() bool {
		calls++
		return true
	}, 10*time.Minute, 10*time.Minute)

	// Concurrent access must not panic.
	done := make(chan struct{})
	for range 10 {
		go func() {
			for range 100 {
				c.Check()
				c.CachedAvailable()
				c.LastChecked()
			}
			done <- struct{}{}
		}()
	}

	for range 10 {
		<-done
	}

	// At least one call was made (race detector in CI will catch issues).
	if calls < 1 || calls > 10*100 {
		t.Errorf("unexpected call count: %d", calls)
	}
}
