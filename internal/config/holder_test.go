package config

import (
	"sync"
	"testing"
)

func TestHolderGetReturnsConstructorValue(t *testing.T) {
	c := Config{BotUser: "archie", PollInterval: Duration(1)}
	h := NewHolder(c)
	if got := h.Get(); got.BotUser != "archie" || got.PollInterval != Duration(1) {
		t.Fatalf("Get() = %+v, want BotUser=archie PollInterval=1", got)
	}
}

func TestHolderSetReplaces(t *testing.T) {
	h := NewHolder(Config{BotUser: "old"})
	h.Set(Config{BotUser: "new"})
	if got := h.Get(); got.BotUser != "new" {
		t.Fatalf("after Set, Get().BotUser = %q, want \"new\"", got.BotUser)
	}
}

func TestHolderConcurrentGetSetIsRaceFree(t *testing.T) {
	// The actual contract is that Get never tears: under -race, no data
	// race, and every observed Config is a value some Set wrote. We do
	// not assert "Get after my Set returns my value" because with N
	// concurrent writers that property is not what Get guarantees -- a
	// later Set can land between this writer's Set and this writer's
	// Get. The race detector is the real check.
	h := NewHolder(Config{BotUser: "v0"})

	const writers = 8
	const iters = 1000

	var wg sync.WaitGroup
	for range writers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range iters {
				h.Set(Config{BotUser: "w"})
			}
		}()
		go func() {
			defer wg.Done()
			for range iters {
				_ = h.Get()
			}
		}()
	}
	wg.Wait()
}

func TestHolderGetPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Get on nil Holder did not panic")
		}
	}()
	var h *Holder
	_ = h.Get()
}

func TestHolderSharedReferenceField(t *testing.T) {
	// Documents the immutability invariant: maps inside the snapshot
	// are shared with all Get callers. Mutating one is visible to all
	// others -- the contract forbids this, but the test pins the
	// current behaviour so any future change (e.g. copying maps) is
	// a deliberate decision.
	original := Config{Models: map[string]string{"builder": "anthropic/claude"}}
	h := NewHolder(original)
	snapshot := h.Get()
	snapshot.Models["builder"] = "mutated"
	if got := h.Get().Models["builder"]; got != "mutated" {
		t.Fatalf("expected Get to see mutation through shared map, got %q", got)
	}
}
