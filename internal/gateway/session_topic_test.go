package gateway

import (
	"context"
	"testing"
)

// /topic off copied the thread's active session onto the flat key. When
// nothing was cached for that thread it copied the empty string -- and
// resolve treats map presence as authoritative, so it returned "" from then
// on, permanently. Every later message in the chat persisted under session
// ID "", history was shared with any other poisoned chat, and the turn-lane
// key became "" so /stop in one chat cancelled a turn in another.
//
// Reachable as a first message on the webhook and email channels, which call
// Route directly without resolving a session first.
func TestTopicOffDoesNotPoisonTheCache(t *testing.T) {
	tests := []struct {
		name string
		// preResolve simulates a channel that resolves before routing (as
		// telegram's submitTurn does) versus one that does not.
		preResolve bool
	}{
		{name: "no session resolved yet"},
		{name: "session already resolved", preResolve: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewSessionStoreMemory("test-node")
			t.Cleanup(func() { _ = store.Close() })

			r := NewRouter(nil, nil, "telegram")
			r.InitSessions(store)
			r.Identity = "archie"

			var want string
			if tc.preResolve {
				id, err := r.sessionTracker.resolve(ctx, "telegram", "", "chat-x", "")
				if err != nil {
					t.Fatalf("resolve: %v", err)
				}
				want = id
			}

			if _, err := r.Route(ctx, Message{Text: "/topic off", ChannelID: "chat-x"}); err != nil {
				t.Fatalf("Route(/topic off): %v", err)
			}

			got, err := r.sessionTracker.resolve(ctx, "telegram", "", "chat-x", "")
			if err != nil {
				t.Fatalf("resolve after /topic off: %v", err)
			}
			if got == "" {
				t.Fatal("resolve returned an empty session ID: the cache was poisoned " +
					"and every later message would persist under session \"\"")
			}
			if tc.preResolve && got != want {
				t.Errorf("resolve = %q, want the session that was already active %q", got, want)
			}

			// Sticky check: the damage previously survived repeat calls
			// because cacheActive also returned the empty existing value.
			again, err := r.sessionTracker.resolve(ctx, "telegram", "", "chat-x", "")
			if err != nil {
				t.Fatalf("resolve (repeat): %v", err)
			}
			if again != got {
				t.Errorf("resolve is unstable: %q then %q", got, again)
			}
		})
	}
}

// An empty session ID is never a meaningful value to cache. Rejecting it at
// the setter means no caller can poison the map, whatever it computes.
func TestSetActiveRejectsAnEmptySession(t *testing.T) {
	store := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = store.Close() })
	tr := newSessionTracker(store)

	tr.setActive("chat-1", "", "real-session")
	tr.setActive("chat-1", "", "")

	if got := tr.getActive("chat-1", ""); got != "real-session" {
		t.Errorf("getActive = %q, want the previous value %q: an empty set must not "+
			"overwrite a real session", got, "real-session")
	}

	tr.setActive("chat-2", "", "")
	if got := tr.getActive("chat-2", ""); got != "" {
		t.Errorf("getActive = %q, want empty", got)
	}
	// The key must be absent, not present-and-empty: resolve treats presence
	// as authoritative.
	id, err := tr.resolve(context.Background(), "telegram", "", "chat-2", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id == "" {
		t.Fatal("resolve returned empty: an empty setActive left a poisoned entry")
	}
}

// /branch with no active session sliced an empty parent ID for its default
// title and panicked. Telegram resolves a session before routing, so it never
// reached this -- but the email and webhook channels call Route directly, and
// the email path runs in a goroutine with no recover, so one message took the
// daemon down.
func TestBranchWithNoActiveSessionDoesNotPanic(t *testing.T) {
	store := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = store.Close() })

	r := NewRouter(nil, nil, "email")
	r.InitSessions(store)
	r.Identity = "archie"

	reply, err := r.Route(context.Background(), Message{Text: "/branch", ChannelID: "inbox-1"})
	if err != nil {
		t.Fatalf("Route(/branch): %v", err)
	}
	if reply == "" {
		t.Fatal("no reply; the user is told nothing")
	}
}
