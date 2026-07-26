package gateway

import (
	"testing"
	"time"
)

func TestSessionSource_Fields(t *testing.T) {
	ss := SessionSource{
		Platform:  "telegram",
		BotUser:   "archie",
		ChannelID: "chat-42",
	}
	if ss.Platform != "telegram" {
		t.Errorf("expected Platform telegram, got %s", ss.Platform)
	}
	if ss.BotUser != "archie" {
		t.Errorf("expected BotUser archie, got %s", ss.BotUser)
	}
	if ss.ChannelID != "chat-42" {
		t.Errorf("expected ChannelID chat-42, got %s", ss.ChannelID)
	}
}

func TestSessionSource_DistinctBots(t *testing.T) {
	// Same platform and channel, different bots → distinct sessions.
	a := SessionSource{Platform: "discord", BotUser: "archie", ChannelID: "general"}
	b := SessionSource{Platform: "discord", BotUser: "winter", ChannelID: "general"}

	if a.Platform != b.Platform {
		t.Error("platforms should match")
	}
	if a.BotUser == b.BotUser {
		t.Error("bot users should differ")
	}
}

func TestSessionContext_Fields(t *testing.T) {
	now := time.Now()
	sc := SessionContext{
		SessionID: "sess-abc",
		Source: SessionSource{
			Platform:  "web",
			BotUser:   "archie",
			ChannelID: "ui-1",
		},
		CreatedAt:    now,
		LastActiveAt: now,
	}
	if sc.SessionID != "sess-abc" {
		t.Errorf("expected SessionID sess-abc, got %s", sc.SessionID)
	}
	if sc.Source.Platform != "web" {
		t.Errorf("expected Source.Platform web, got %s", sc.Source.Platform)
	}
	if sc.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if sc.LastActiveAt.IsZero() {
		t.Error("expected non-zero LastActiveAt")
	}
}

func TestSessionContext_ZeroValue(t *testing.T) {
	var sc SessionContext
	if sc.SessionID != "" {
		t.Error("expected empty SessionID on zero value")
	}
	if !sc.CreatedAt.IsZero() {
		t.Error("expected zero CreatedAt on zero value")
	}
	if !sc.LastActiveAt.IsZero() {
		t.Error("expected zero LastActiveAt on zero value")
	}
	if sc.Source.Platform != "" {
		t.Error("expected empty Source.Platform on zero value")
	}
	if sc.Source.BotUser != "" {
		t.Error("expected empty Source.BotUser on zero value")
	}
	if sc.Source.ChannelID != "" {
		t.Error("expected empty Source.ChannelID on zero value")
	}
	if sc.Source.ThreadID != "" {
		t.Error("expected empty Source.ThreadID on zero value")
	}
}

func TestSessionSource_DistinctThreads(t *testing.T) {
	// Same platform, bot, and channel, different threads → distinct sessions.
	a := SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "-100123", ThreadID: "5"}
	b := SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "-100123", ThreadID: "8"}

	if a.Platform != b.Platform {
		t.Error("platforms should match")
	}
	if a.ChannelID != b.ChannelID {
		t.Error("channel IDs should match")
	}
	if a.ThreadID == b.ThreadID {
		t.Error("thread IDs should differ")
	}

	// ThreadID empty for flat chats (General topic).
	c := SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "-100123"}
	if c.ThreadID != "" {
		t.Errorf("expected empty ThreadID for flat chat, got %q", c.ThreadID)
	}

	// A thread session and a flat-chat session on the same channel
	// must be distinguishable.
	d := SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "-100123", ThreadID: "5"}
	if d.ThreadID != "5" {
		t.Errorf("expected ThreadID 5, got %q", d.ThreadID)
	}
}
