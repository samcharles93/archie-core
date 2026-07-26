package events

import (
	"testing"
	"time"
)

func TestBusFanOutAndDrop(t *testing.T) {
	b := NewBus()
	defer b.Close()

	fast := b.Subscribe(8)
	slow := b.Subscribe(2) // will overflow

	for i := range 5 {
		b.Publish(Event{Kind: KindLog, Issue: i})
	}

	if got := len(fast.C); got != 5 {
		t.Fatalf("fast subscriber buffered %d, want 5", got)
	}
	if got := len(slow.C); got != 2 {
		t.Fatalf("slow subscriber buffered %d, want 2", got)
	}
	if d := slow.Dropped(); d != 3 {
		t.Fatalf("slow.Dropped() = %d, want 3", d)
	}

	e := <-fast.C
	if e.At.IsZero() {
		t.Fatal("Publish must stamp At")
	}

	fast.Close()
	b.Publish(Event{Kind: KindLog}) // must not panic on closed subscriber
	if got := len(slow.C); got != 2 {
		t.Fatalf("closed subscriber removal broke fan-out: slow has %d", got)
	}
}

func TestBusCloseStopsDelivery(t *testing.T) {
	b := NewBus()
	s := b.Subscribe(4)
	b.Close()
	b.Publish(Event{Kind: KindLog}) // no-op, no panic
	if _, open := <-s.C; open {
		t.Fatal("subscriber channel must be closed after bus Close")
	}
}

// ── Regression tests ───────────────────────────────────────────────

func TestSubscribeZeroBufferDefaults(t *testing.T) {
	b := NewBus()
	defer b.Close()
	s := b.Subscribe(0)
	// Send 64 events (default buffer size) — none should be dropped.
	for i := range 64 {
		b.Publish(Event{Kind: KindLog, Issue: i})
	}
	if d := s.Dropped(); d != 0 {
		t.Errorf("0-buffer subscriber should default to 64, but dropped %d", d)
	}
}

func TestSubscribeNegativeBufferDefaults(t *testing.T) {
	b := NewBus()
	defer b.Close()
	s := b.Subscribe(-1)
	for i := range 64 {
		b.Publish(Event{Kind: KindLog, Issue: i})
	}
	if d := s.Dropped(); d != 0 {
		t.Errorf("negative-buffer subscriber should default to 64, but dropped %d", d)
	}
}

func TestSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	b := NewBus()
	b.Close()
	s := b.Subscribe(10)
	if _, open := <-s.C; open {
		t.Error("subscribe after close should return closed channel")
	}
}

func TestDoubleCloseIsSafe(t *testing.T) {
	b := NewBus()
	b.Close()
	// Must not panic.
	b.Close()
}

func TestPublishDuringCloseConcurrent(t *testing.T) {
	b := NewBus()
	s := b.Subscribe(100)
	done := make(chan struct{})
	go func() {
		for range 50 {
			b.Publish(Event{Kind: KindLog})
		}
		close(done)
	}()
	b.Close()
	<-done
	// Drain channel — all published events plus eventual close.
	for range s.C {
	}
	// Must not panic.
}

func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	b := NewBus()
	defer b.Close()

	slow := b.Subscribe(1)   // tiny buffer
	fast := b.Subscribe(100) // large buffer

	// Publish enough to overflow slow subscriber.
	for i := range 50 {
		b.Publish(Event{Kind: KindLog, Issue: i})
	}

	// Fast subscriber should have received all 50 (or at least most).
	// The key assertion: fast subscriber was not blocked by slow.
	if len(fast.C) < 40 {
		t.Errorf("fast subscriber only got %d events; slow should not block fast", len(fast.C))
	}
	if slow.Dropped() == 0 {
		t.Error("slow subscriber with buffer 1 should have dropped events")
	}
}

func TestMultiSubscriberOrdering(t *testing.T) {
	b := NewBus()
	defer b.Close()

	a := b.Subscribe(10)
	bSub := b.Subscribe(10)

	b.Publish(Event{Kind: KindLog, Issue: 1})
	b.Publish(Event{Kind: KindLog, Issue: 2})
	b.Publish(Event{Kind: KindLog, Issue: 3})

	// Both subscribers should receive events in order.
	for _, sub := range []*Sub{a, bSub} {
		if e := <-sub.C; e.Issue != 1 {
			t.Errorf("expected issue 1, got %d", e.Issue)
		}
		if e := <-sub.C; e.Issue != 2 {
			t.Errorf("expected issue 2, got %d", e.Issue)
		}
		if e := <-sub.C; e.Issue != 3 {
			t.Errorf("expected issue 3, got %d", e.Issue)
		}
	}
}

func TestSubscriberCloseRemovesFromBus(t *testing.T) {
	b := NewBus()
	defer b.Close()

	s := b.Subscribe(10)
	s.Close()

	// Publish after close — must not block or panic.
	b.Publish(Event{Kind: KindLog})
	// Channel should be closed.
	if _, open := <-s.C; open {
		t.Error("channel should be closed after Sub.Close()")
	}
}

func TestPublishStampsAt(t *testing.T) {
	b := NewBus()
	defer b.Close()
	s := b.Subscribe(1)

	before := time.Now().UTC()
	b.Publish(Event{Kind: KindLog})
	after := time.Now().UTC()

	e := <-s.C
	if e.At.Before(before) || e.At.After(after) {
		t.Errorf("At = %v, expected between %v and %v", e.At, before, after)
	}
}

func TestPublishDoesNotOverwritePreSetAt(t *testing.T) {
	b := NewBus()
	defer b.Close()
	s := b.Subscribe(1)

	preset := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	b.Publish(Event{Kind: KindLog, At: preset})

	e := <-s.C
	if !e.At.Equal(preset) {
		t.Errorf("At = %v, want %v (preset should not be overwritten)", e.At, preset)
	}
}
