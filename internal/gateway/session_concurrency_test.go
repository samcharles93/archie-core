package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestSessionTrackerConcurrentResolve pins that the active-session map is
// safe under the concurrent dispatch the Telegram gateway actually uses:
// go-telegram/bot runs each update in its own goroutine. Run with -race.
func TestSessionTrackerConcurrentResolve(t *testing.T) {
	tests := []struct {
		name     string
		channels int
		workers  int
	}{
		{"single channel contended", 1, 32},
		{"many channels", 16, 32},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewSessionStoreMemory("test-node")
			t.Cleanup(func() { _ = store.Close() })
			tracker := newSessionTracker(store)

			var wg sync.WaitGroup
			for i := range tc.workers {
				wg.Go(func() {
					channel := fmt.Sprintf("chan-%d", i%tc.channels)
					if _, err := tracker.resolve(ctx, "telegram", "bot", channel, ""); err != nil {
						t.Errorf("resolve: %v", err)
					}
					tracker.setActive(channel, "", "override-"+channel)
					_ = tracker.getActive(channel, "")
				})
			}
			wg.Wait()
		})
	}
}

// TestSessionTrackerResolveIsStable pins that resolve returns the same
// session for the same channel+thread across concurrent callers, so
// interleaved messages cannot land in different sessions.
func TestSessionTrackerResolveIsStable(t *testing.T) {
	ctx := context.Background()
	store := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = store.Close() })
	tracker := newSessionTracker(store)

	const workers = 32
	ids := make([]string, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			id, err := tracker.resolve(ctx, "telegram", "bot", "chan", "thread")
			if err != nil {
				t.Errorf("resolve: %v", err)
				return
			}
			ids[i] = id
		})
	}
	wg.Wait()

	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("worker %d resolved %q, worker 0 resolved %q", i, id, ids[0])
		}
	}
}

// TestCanonicalMessageIDIsSessionScoped pins that the same upstream message
// copied into another session gets its own identity, so branch lineage does
// not alias across conversations.
func TestCanonicalMessageIDIsSessionScoped(t *testing.T) {
	a := newMessageID("session-a", "tg-1")
	b := newMessageID("session-b", "tg-1")
	if a == b {
		t.Errorf("sessions a and b both derived %q for the same upstream ID", a)
	}
	if again := newMessageID("session-a", "tg-1"); again != a {
		t.Errorf("derivation is not deterministic: %q then %q", a, again)
	}
	if first, second := newMessageID("session-a", ""), newMessageID("session-a", ""); first == second {
		t.Error("messages with no upstream ID must get distinct identities")
	}
}
