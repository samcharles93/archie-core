package gateway

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestResolveSessionKeyStampsTheMessagesPlatform is archie-core-c1qx's contract:
// a session's platform is the channel that carried the message, not the
// Gateway's own name. One Router serves every channel, so its own name is "web"
// even for a Telegram turn, which made Source.Platform -- the first component of
// the session natural key -- a constant, and left the per-user identity policy
// unreachable with a real channel name.
func TestResolveSessionKeyStampsTheMessagesPlatform(t *testing.T) {
	store := NewSessionStoreMemory()
	t.Cleanup(func() { _ = store.Close() })
	router := NewRouter(nil, nil, "web")
	router.Identity = "archie"
	router.InitSessions(store)
	ctx := context.Background()

	telegram := Inbound{Platform: "telegram", Message: messaging.Message{
		ConversationID: messaging.ConversationID{ChannelID: "-100123"},
		SenderID:       "42",
		Role:           messaging.RoleUser,
		Text:           "hello",
	}}
	id, err := router.ResolveSessionKey(ctx, telegram)
	if err != nil {
		t.Fatalf("ResolveSessionKey: %v", err)
	}
	session, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if session.Source.Platform != "telegram" {
		t.Errorf("Source.Platform = %q, want %q: the channel that carried the message, not the Gateway's own name",
			session.Source.Platform, "telegram")
	}
	// The platform is the first component of the session lookup key, so a lookup
	// under the channel's name is what a later message in the same chat resolves
	// through. Under the Gateway's name it would miss and start a second session.
	found, err := store.GetByChannel(ctx, "telegram", "-100123")
	if err != nil {
		t.Fatalf("GetByChannel: %v", err)
	}
	if len(found) != 1 || found[0].SessionID != id {
		t.Errorf("GetByChannel(telegram, -100123) = %+v, want the session just resolved", found)
	}

	// A frontend that does not name its channel keeps the Gateway's own name,
	// which is the behaviour every existing session row has.
	plain := Inbound{Message: messaging.Message{
		ConversationID: messaging.ConversationID{ChannelID: "-100999"},
		Role:           messaging.RoleUser,
		Text:           "no platform",
	}}
	plainID, err := router.ResolveSessionKey(ctx, plain)
	if err != nil {
		t.Fatalf("ResolveSessionKey(no platform): %v", err)
	}
	plainSession, err := store.Get(ctx, plainID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if plainSession.Source.Platform != "web" {
		t.Errorf("Source.Platform = %q, want the Gateway's own name %q when the sender names none",
			plainSession.Source.Platform, "web")
	}
}
