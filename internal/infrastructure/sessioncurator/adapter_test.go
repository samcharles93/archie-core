package sessioncurator

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/gateway"
)

func newTestStore(t *testing.T) gateway.SessionStore {
	t.Helper()
	s := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAdapterRecentSessionsFiltersByActivity(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()

	old := gateway.SessionContext{SessionID: "old", LastActiveAt: time.Unix(100, 0)}
	recent := gateway.SessionContext{SessionID: "recent", LastActiveAt: time.Unix(2000, 0)}
	if err := store.Save(ctx, old); err != nil {
		t.Fatalf("Save(old) = %v", err)
	}
	if err := store.Save(ctx, recent); err != nil {
		t.Fatalf("Save(recent) = %v", err)
	}

	a := NewAdapter(store, "archie")
	got, err := a.RecentSessions(ctx, time.Unix(1000, 0))
	if err != nil {
		t.Fatalf("RecentSessions() = %v, want nil", err)
	}
	if len(got) != 1 || got[0].ID != "recent" {
		t.Fatalf("RecentSessions() = %v, want only 'recent'", got)
	}
}

func TestAdapterMessagesDerivesRoleFromBotUser(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()

	sess := gateway.SessionContext{SessionID: "s1", LastActiveAt: time.Unix(1000, 0)}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if err := store.SaveMessage(ctx, "s1", gateway.Message{From: "user123", Text: "hello", At: time.Unix(1, 0)}); err != nil {
		t.Fatalf("SaveMessage(user) = %v", err)
	}
	if err := store.SaveMessage(ctx, "s1", gateway.Message{From: "archie", Text: "hi there", At: time.Unix(2, 0)}); err != nil {
		t.Fatalf("SaveMessage(bot) = %v", err)
	}

	a := NewAdapter(store, "archie")
	got, err := a.Messages(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("Messages() = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("Messages() = %v, want 2", got)
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Errorf("Messages()[0] = %+v, want user/hello", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "hi there" {
		t.Errorf("Messages()[1] = %+v, want assistant/hi there", got[1])
	}
}
