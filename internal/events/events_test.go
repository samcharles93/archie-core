package events

import "testing"

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
