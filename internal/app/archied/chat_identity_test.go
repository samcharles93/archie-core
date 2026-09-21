package archied

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestWebhookUserIdentityNeverReturnsTheRoutePath guards
// docs/prds/memory-engine-unification.md §3 and §4's specific webhook
// requirement: internal/channels/webhook/webhook.go carries the configured route
// path in Inbound.BudgetKey, never in SenderID, and no platform may turn one into
// a user identity however plausible it looks as one.
func TestWebhookUserIdentityNeverReturnsTheRoutePath(t *testing.T) {
	resolver := userIdentityResolver()
	id, ok := resolver(messaging.Inbound{
		Platform: "webhook",
		Message:  messaging.Message{SenderID: "/github", Sender: "webhook"},
	})
	if ok || id != "" {
		t.Fatalf("webhook resolver = (%q, %v), want (\"\", false)", id, ok)
	}
}

// TestUserIdentityResolverPerPlatform documents the composition's per-platform
// choice: telegram and email carry a native sender id, the dashboard ("web") and
// webhook do not, and a platform nobody has decided about fails closed.
//
// It is keyed on the message's platform rather than a resolver per channel,
// because one Gateway Router serves every channel: closing over a channel name
// resolved every turn as "web" (archie-core-c1qx).
func TestUserIdentityResolverPerPlatform(t *testing.T) {
	tests := []struct {
		platform string
		senderID string
		wantOK   bool
	}{
		{"telegram", "12345", true},
		{"email", "person@example.com", true},
		{"web", "anything", false},
		{"webhook", "/github", false},
		{"unknown-future-channel", "anything", false},
		{"", "anything", false},
	}
	resolver := userIdentityResolver()
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			id, ok := resolver(messaging.Inbound{Platform: tt.platform, Message: messaging.Message{SenderID: tt.senderID}})
			if ok != tt.wantOK {
				t.Fatalf("%s resolver ok = %v, want %v", tt.platform, ok, tt.wantOK)
			}
			if ok && string(id) != tt.senderID {
				t.Fatalf("%s resolver id = %q, want %q", tt.platform, id, tt.senderID)
			}
		})
	}
}

// TestUserIdentityResolverFailsClosedOnEmptySenderID guards §3's "fail closed":
// an empty SenderID (a channel that carries no per-caller id for this particular
// message) must resolve to no identity, never an empty-string identity that
// Subject would treat as present.
func TestUserIdentityResolverFailsClosedOnEmptySenderID(t *testing.T) {
	resolver := userIdentityResolver()
	id, ok := resolver(messaging.Inbound{Platform: "telegram", Message: messaging.Message{SenderID: ""}})
	if ok || id != "" {
		t.Fatalf("telegram resolver with no sender id = (%q, %v), want (\"\", false)", id, ok)
	}
}
