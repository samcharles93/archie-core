package gateway

import (
	"context"
	"testing"
	"time"
)

func TestSessionStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	sc := SessionContext{
		SessionID: "sess-1",
		Source: SessionSource{
			Platform:  "telegram",
			BotUser:   "archie",
			ChannelID: "chat-42",
		},
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}

	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := st.Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.SessionID != "sess-1" {
		t.Errorf("expected SessionID sess-1, got %s", got.SessionID)
	}
	if got.Source.Platform != "telegram" {
		t.Errorf("expected Platform telegram, got %s", got.Source.Platform)
	}
	if got.Source.BotUser != "archie" {
		t.Errorf("expected BotUser archie, got %s", got.Source.BotUser)
	}
	if got.Source.ChannelID != "chat-42" {
		t.Errorf("expected ChannelID chat-42, got %s", got.Source.ChannelID)
	}
}

func TestSessionStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	got, err := st.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Error("expected nil for missing session")
	}
}

func TestSessionStore_SaveOverwrites(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	sc1 := SessionContext{
		SessionID: "sess-2",
		Source: SessionSource{
			Platform:  "discord",
			BotUser:   "archie",
			ChannelID: "general",
		},
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	if err := st.Save(ctx, sc1); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Overwrite with same ID but different platform.
	sc2 := SessionContext{
		SessionID: "sess-2",
		Source: SessionSource{
			Platform:  "web",
			BotUser:   "winter",
			ChannelID: "ui-1",
		},
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	if err := st.Save(ctx, sc2); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}

	got, err := st.Get(ctx, "sess-2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source.Platform != "web" {
		t.Errorf("expected Platform web after overwrite, got %s", got.Source.Platform)
	}
	if got.Source.BotUser != "winter" {
		t.Errorf("expected BotUser winter after overwrite, got %s", got.Source.BotUser)
	}
}

func TestSessionStore_GetByChannel(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	// Two sessions on the same channel with different bots.
	s1 := SessionContext{
		SessionID: "sess-a",
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-1"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	s2 := SessionContext{
		SessionID: "sess-b",
		Source:    SessionSource{Platform: "telegram", BotUser: "winter", ChannelID: "chat-1"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}
	s3 := SessionContext{
		SessionID: "sess-c",
		Source:    SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-2"},
		CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}

	for _, s := range []SessionContext{s1, s2, s3} {
		if err := st.Save(ctx, s); err != nil {
			t.Fatalf("Save %s: %v", s.SessionID, err)
		}
	}

	// Query chat-1  --  should get both sessions.
	results, err := st.GetByChannel(ctx, "telegram", "chat-1")
	if err != nil {
		t.Fatalf("GetByChannel: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 sessions for chat-1, got %d", len(results))
	}

	// Query chat-2  --  should get one.
	results, err = st.GetByChannel(ctx, "telegram", "chat-2")
	if err != nil {
		t.Fatalf("GetByChannel chat-2: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 session for chat-2, got %d", len(results))
	}

	// Different platform  --  none.
	results, err = st.GetByChannel(ctx, "discord", "chat-1")
	if err != nil {
		t.Fatalf("GetByChannel discord: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 sessions for discord, got %d", len(results))
	}
}

func TestSessionStore_Delete(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	sc := SessionContext{
		SessionID:    "sess-del",
		Source:       SessionSource{Platform: "web", BotUser: "archie", ChannelID: "ui-1"},
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := st.Delete(ctx, "sess-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := st.Get(ctx, "sess-del")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}

	// Delete non-existent is a no-op.
	if err := st.Delete(ctx, "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestSessionStore_Touch(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	oldTime := time.Now().Add(-1 * time.Hour)
	sc := SessionContext{
		SessionID:    "sess-touch",
		Source:       SessionSource{Platform: "discord", BotUser: "archie", ChannelID: "dev"},
		CreatedAt:    oldTime,
		LastActiveAt: oldTime,
	}
	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := st.Touch(ctx, "sess-touch"); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	got, err := st.Get(ctx, "sess-touch")
	if err != nil {
		t.Fatalf("Get after touch: %v", err)
	}
	if got == nil {
		t.Fatal("expected session after touch")
	}
	// LastActiveAt should be updated to near-now (after the old time).
	if !got.LastActiveAt.After(oldTime) {
		t.Error("expected LastActiveAt to be after oldTime after Touch")
	}
	// CreatedAt should not change.
	if got.CreatedAt.Sub(oldTime).Abs() > time.Second {
		t.Errorf("expected CreatedAt to be preserved, was %v vs %v", got.CreatedAt, oldTime)
	}

	// Touch non-existent session  --  should not error.
	if err := st.Touch(ctx, "nonexistent"); err != nil {
		t.Errorf("Touch nonexistent: %v", err)
	}
}

func TestSessionStore_SaveWithThreadID(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	sc := SessionContext{
		SessionID: "sess-thread-1",
		Source: SessionSource{
			Platform:  "telegram",
			BotUser:   "archie",
			ChannelID: "-100123",
			ThreadID:  "5",
		},
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}

	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := st.Get(ctx, "sess-thread-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Source.ThreadID != "5" {
		t.Errorf("expected ThreadID 5, got %q", got.Source.ThreadID)
	}
	if got.Source.ChannelID != "-100123" {
		t.Errorf("expected ChannelID -100123, got %q", got.Source.ChannelID)
	}
}

func TestSessionStore_SaveWithoutThreadID(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	// ThreadID is empty for flat chats — round-trip should preserve that.
	sc := SessionContext{
		SessionID: "sess-flat",
		Source: SessionSource{
			Platform:  "telegram",
			BotUser:   "archie",
			ChannelID: "-100456",
		},
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}

	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := st.Get(ctx, "sess-flat")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source.ThreadID != "" {
		t.Errorf("expected empty ThreadID, got %q", got.Source.ThreadID)
	}
}

func TestSessionStore_List(t *testing.T) {
	ctx := context.Background()
	st := NewSessionStoreMemory("test-node")
	defer func() { _ = st.Close() }()

	// Empty list.
	all, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(all))
	}

	// Add a couple.
	for i, id := range []string{"s1", "s2", "s3"} {
		sc := SessionContext{
			SessionID:    id,
			Source:       SessionSource{Platform: "web", BotUser: "archie", ChannelID: "ch-" + id},
			CreatedAt:    time.Now().Add(time.Duration(i) * time.Minute),
			LastActiveAt: time.Now(),
		}
		if err := st.Save(ctx, sc); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	all, err = st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(all))
	}
}
