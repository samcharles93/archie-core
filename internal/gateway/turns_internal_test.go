package gateway

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestTurnsRetiresIdleSessionLanes(t *testing.T) {
	t.Parallel()

	turns := NewTurns(slog.New(slog.DiscardHandler))
	const sessions = 100
	done := make(chan struct{}, sessions)
	for i := range sessions {
		turns.Submit(context.Background(), string(rune(i)), func(context.Context) {
			done <- struct{}{}
		})
	}
	for range sessions {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("submitted turn did not finish")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		turns.mu.Lock()
		remaining := len(turns.lanes)
		turns.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d idle session lanes remain allocated", remaining)
		}
		time.Sleep(time.Millisecond)
	}

	// A retired session must remain reusable; cleanup cannot strand a turn
	// between looking up the old lane and creating its replacement.
	reused := make(chan struct{})
	turns.Submit(context.Background(), string(rune(0)), func(context.Context) { close(reused) })
	select {
	case <-reused:
	case <-time.After(2 * time.Second):
		t.Fatal("retired session did not accept a new turn")
	}
}
