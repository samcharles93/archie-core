package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// deleteFailingStore is a session store whose Delete always fails, used to
// prove the command surfaces a store failure instead of reporting success.
type deleteFailingStore struct {
	*fakeSessionStore
	err error
}

func (d *deleteFailingStore) Delete(context.Context, string) error { return d.err }

// newDeleteRouter returns a router wired to a fresh in-memory store,
// plus that store, so tests can assert on what survived the command.
func newDeleteRouter(t *testing.T) (*Router, *fakeSessionStore) {
	t.Helper()
	store := newFakeSessionStore()
	r := NewRouter(nil, nil, "test-gw")
	r.InitSessions(store)
	return r, store
}

// seedSession stores a session on channelID and returns its ID.
func seedSession(t *testing.T, r *Router, store *fakeSessionStore, id, channelID, title string) string {
	t.Helper()
	sc := SessionContext{
		SessionID: id,
		Source: SessionSource{
			Platform:  r.gatewayName,
			BotUser:   r.Identity,
			ChannelID: channelID,
		},
		Title: title,
	}
	if err := store.Save(context.Background(), sc); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
	return id
}

func TestRouteDeleteNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/delete abc-123"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

func TestRouteDeleteNoArgExplainsUsageAndDeletesNothing(t *testing.T) {
	r, store := newDeleteRouter(t)
	seedSession(t, r, store, "abc-123", "chan-1", "Work")

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage") {
		t.Errorf("reply = %q, want usage", reply)
	}
	if got, _ := store.Get(context.Background(), "abc-123"); got == nil {
		t.Error("bare /delete removed a session")
	}
}

func TestRouteDeleteRejectsUnknownReference(t *testing.T) {
	r, store := newDeleteRouter(t)
	seedSession(t, r, store, "abc-123", "chan-1", "Work")

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete zzzz"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No session matching") {
		t.Errorf("reply = %q, want 'No session matching'", reply)
	}
	if got, _ := store.Get(context.Background(), "abc-123"); got == nil {
		t.Error("a non-matching reference removed a session")
	}
}

func TestRouteDeleteRejectsAmbiguousReference(t *testing.T) {
	r, store := newDeleteRouter(t)
	seedSession(t, r, store, "abcd-123", "chan-1", "One")
	seedSession(t, r, store, "abcd-456", "chan-1", "Two")

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete abcd"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Multiple sessions") {
		t.Errorf("reply = %q, want 'Multiple sessions'", reply)
	}
	for _, id := range []string{"abcd-123", "abcd-456"} {
		if got, _ := store.Get(context.Background(), id); got == nil {
			t.Errorf("ambiguous reference deleted %s", id)
		}
	}
}

// A prefix short enough to be a typo must not destroy a conversation, even
// when it happens to match exactly one session. /resume has no such guard
// because resuming the wrong session is undone by resuming the right one.
func TestRouteDeleteRejectsShortPrefixEvenWhenUnique(t *testing.T) {
	r, store := newDeleteRouter(t)
	seedSession(t, r, store, "abc-123", "chan-1", "Work")

	for _, ref := range []string{"a", "ab", "abc"} {
		reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete " + ref})
		if err != nil {
			t.Fatalf("Route %q: %v", ref, err)
		}
		if !strings.Contains(reply, "characters") {
			t.Errorf("/delete %s = %q, want a reply asking for more characters", ref, reply)
		}
		if got, _ := store.Get(context.Background(), "abc-123"); got == nil {
			t.Fatalf("/delete %s deleted the session on a short prefix", ref)
		}
	}
}

// The length guard applies to prefix matching only: a session whose whole
// ID is short is still deletable by naming it in full.
func TestRouteDeleteAcceptsShortExactID(t *testing.T) {
	r, store := newDeleteRouter(t)
	seedSession(t, r, store, "abc", "chan-1", "Work")

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete abc"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Deleted session") {
		t.Errorf("reply = %q, want 'Deleted session'", reply)
	}
	if got, _ := store.Get(context.Background(), "abc"); got != nil {
		t.Error("exact short ID was not deleted")
	}
}

// An exact ID wins over a prefix that also matches other sessions, matching
// how /resume resolves a reference.
func TestRouteDeleteExactIDBeatsAmbiguousPrefix(t *testing.T) {
	r, store := newDeleteRouter(t)
	seedSession(t, r, store, "abcd-123", "chan-1", "One")
	seedSession(t, r, store, "abcd-123-extra", "chan-1", "Two")

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete abcd-123"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Deleted session") {
		t.Fatalf("reply = %q, want 'Deleted session'", reply)
	}
	if got, _ := store.Get(context.Background(), "abcd-123"); got != nil {
		t.Error("the exactly named session survived")
	}
	if got, _ := store.Get(context.Background(), "abcd-123-extra"); got == nil {
		t.Error("the session sharing the prefix was deleted too")
	}
}

func TestRouteDeleteRemovesSessionAndItsHistory(t *testing.T) {
	r, store := newDeleteRouter(t)
	id := seedSession(t, r, store, "abcd-123", "chan-1", "Work")
	for _, text := range []string{"first", "second"} {
		if err := store.SaveMessage(context.Background(), id, Message{From: "sam", Text: text}); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete " + id})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Deleted session") || !strings.Contains(reply, "Work") {
		t.Errorf("reply = %q, want the deleted session's title", reply)
	}
	if got, _ := store.Get(context.Background(), id); got != nil {
		t.Error("session survived /delete")
	}
	count, err := store.MessageCount(context.Background(), id)
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("history left %d messages behind", count)
	}
}

// Deleting the session a chat is currently using must clear the tracker
// too: a cached pointer at a deleted session would persist every later
// message under an ID the store no longer knows.
func TestRouteDeleteActiveSessionClearsTracker(t *testing.T) {
	r, store := newDeleteRouter(t)
	msg := Message{ChannelID: "chan-1"}
	if _, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/new Work"}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	id := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	if id == "" {
		t.Fatal("/new did not set an active session")
	}

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete " + id})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "new conversation") {
		t.Errorf("reply = %q, want a note that the next message starts fresh", reply)
	}
	if active := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID); active != "" {
		t.Errorf("tracker still points at %q after deleting it", active)
	}

	// The next turn must resolve to a session that exists in the store.
	next, err := r.ResolveSessionKey(context.Background(), msg)
	if err != nil {
		t.Fatalf("ResolveSessionKey: %v", err)
	}
	if next == id {
		t.Errorf("resolved the deleted session %q again", id)
	}
	if got, _ := store.Get(context.Background(), next); got == nil {
		t.Errorf("resolved session %q is not in the store", next)
	}
}

// Deleting some other session must leave the current conversation alone.
func TestRouteDeleteOtherSessionKeepsActive(t *testing.T) {
	r, store := newDeleteRouter(t)
	other := seedSession(t, r, store, "abcd-999", "chan-1", "Old")
	if _, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/new Current"}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	active := r.sessionTracker.getActive("chan-1", "")

	reply, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete " + other})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if strings.Contains(reply, "new conversation") {
		t.Errorf("reply = %q, should not claim the active conversation ended", reply)
	}
	if got := r.sessionTracker.getActive("chan-1", ""); got != active {
		t.Errorf("active session = %q, want %q", got, active)
	}
}

func TestRouteDeleteReportsStoreFailure(t *testing.T) {
	store := newFakeSessionStore()
	r := NewRouter(nil, nil, "test-gw")
	r.InitSessions(store)
	seedSession(t, r, store, "abcd-123", "chan-1", "Work")
	r.Sessions = &deleteFailingStore{fakeSessionStore: store, err: errors.New("store unavailable")}

	_, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete abcd-123"})
	if err == nil {
		t.Fatal("expected an error when the store cannot delete")
	}
}

func TestRouteDeleteReportsListFailure(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	r.Sessions = &fakeSessionLister{
		fakeSessionStore: newFakeSessionStore(),
		err:              errors.New("store unavailable"),
	}

	_, err := r.Route(context.Background(), Message{ChannelID: "chan-1", Text: "/delete abcd-123"})
	if err == nil {
		t.Fatal("expected an error when the store cannot list sessions")
	}
}

// forget must clear every channel+thread pointing at the session, not just
// the one the command was typed in: the same session can be active in a
// flat chat and in a thread at once.
func TestSessionTrackerForgetClearsEveryReference(t *testing.T) {
	tr := newSessionTracker(newFakeSessionStore())
	tr.setActive("chan-1", "", "doomed")
	tr.setActive("chan-1", "thread-1", "doomed")
	tr.setActive("chan-2", "", "survivor")

	tr.forget("doomed")

	for _, key := range []struct{ channel, thread string }{{"chan-1", ""}, {"chan-1", "thread-1"}} {
		if got := tr.getActive(key.channel, key.thread); got != "" {
			t.Errorf("getActive(%q, %q) = %q, want empty", key.channel, key.thread, got)
		}
	}
	if got := tr.getActive("chan-2", ""); got != "survivor" {
		t.Errorf("getActive(chan-2) = %q, want survivor", got)
	}
}

// An empty argument must not be treated as a session reference: it is a
// prefix of every ID.
func TestSessionTrackerForgetIgnoresEmptyID(t *testing.T) {
	tr := newSessionTracker(newFakeSessionStore())
	tr.setActive("chan-1", "", "keep-me")

	tr.forget("")

	if got := tr.getActive("chan-1", ""); got != "keep-me" {
		t.Errorf("getActive = %q, want keep-me", got)
	}
}

func TestDeleteIsAPublishedLocalCommand(t *testing.T) {
	found := false
	for _, spec := range LocalCommandSpecs() {
		if spec.Command == "/delete" {
			found = true
			if !strings.Contains(spec.Usage, "<session-id>") {
				t.Errorf("/delete usage %q does not name its argument", spec.Usage)
			}
		}
	}
	if !found {
		t.Error("LocalCommandSpecs() does not advertise /delete")
	}
}
