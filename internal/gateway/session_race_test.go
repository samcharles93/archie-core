package gateway

import (
	"context"
	"testing"
)

// hookStore fires a callback immediately after GetByChannel returns, which is
// the point inside resolve where a racing command can land.
type hookStore struct {
	SessionStore
	afterGetByChannel func()
}

func (h *hookStore) GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error) {
	out, err := h.SessionStore.GetByChannel(ctx, platform, channelID)
	if h.afterGetByChannel != nil {
		fn := h.afterGetByChannel
		h.afterGetByChannel = nil // fire once; the callback itself resolves
		fn()
	}
	return out, err
}

// resolve saved its new session to the store before asking the cache who won,
// so the loser of that race left a session behind that nothing points at. It
// still appeared in /topic and /sessions listings as "(untitled)", and with
// recency-based resolution it could be resurrected on a later restart.
//
// The daemon really does run commands on lane goroutines while submitTurn
// resolves on the channel worker, so this interleaving is reachable.
func TestResolveDoesNotOrphanASessionItLoses(t *testing.T) {
	ctx := context.Background()
	inner := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = inner.Close() })
	store := &hookStore{SessionStore: inner}

	r := NewRouter(nil, nil, "telegram")
	r.InitSessions(store)
	r.Identity = "archie"

	// While resolve is between its store lookup and its own write, /new
	// creates a session and makes it active.
	var newSessionID string
	store.afterGetByChannel = func() {
		if _, err := r.Route(ctx, Message{Text: "/new", ChannelID: "chat-y"}); err != nil {
			t.Errorf("Route(/new): %v", err)
			return
		}
		newSessionID = r.sessionTracker.getActive("chat-y", "")
	}

	got, err := r.sessionTracker.resolve(ctx, "telegram", "archie", "chat-y", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if newSessionID == "" {
		t.Fatal("the racing /new did not produce a session; the interleaving did not happen")
	}
	if got != newSessionID {
		t.Errorf("resolve = %q, want the racing winner %q", got, newSessionID)
	}

	sessions, err := inner.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("store holds %d sessions (%v), want only the one that won: "+
			"the loser was persisted and is now unreachable",
			len(sessions), sessionIDs(sessions))
	}
	if sessions[0].SessionID != newSessionID {
		t.Errorf("stored session = %q, want %q", sessions[0].SessionID, newSessionID)
	}
}

// A failed Save must not leave the cache pointing at a session that does not
// exist, or every later message in that chat addresses a phantom.
func TestResolveReleasesItsClaimWhenSaveFails(t *testing.T) {
	ctx := context.Background()
	inner := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = inner.Close() })
	store := &failingSaveStore{SessionStore: inner, fail: true}

	r := NewRouter(nil, nil, "telegram")
	r.InitSessions(store)

	if _, err := r.sessionTracker.resolve(ctx, "telegram", "archie", "chat-z", ""); err == nil {
		t.Fatal("resolve succeeded despite a failing Save")
	}
	if got := r.sessionTracker.getActive("chat-z", ""); got != "" {
		t.Fatalf("getActive = %q after a failed create, want empty: the cache "+
			"points at a session that was never stored", got)
	}

	// With the store working again, the next attempt must succeed.
	store.fail = false
	id, err := r.sessionTracker.resolve(ctx, "telegram", "archie", "chat-z", "")
	if err != nil {
		t.Fatalf("resolve after recovery: %v", err)
	}
	if id == "" {
		t.Fatal("resolve returned an empty session ID after recovery")
	}
}

type failingSaveStore struct {
	SessionStore
	fail bool
}

func (f *failingSaveStore) Save(ctx context.Context, sc SessionContext) error {
	if f.fail {
		return errSaveRefused
	}
	return f.SessionStore.Save(ctx, sc)
}

var errSaveRefused = errTest("save refused")

type errTest string

func (e errTest) Error() string { return string(e) }
