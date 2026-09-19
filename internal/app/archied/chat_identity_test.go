package archied

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestWebhookUserIdentityNeverReturnsTheRoutePath guards
// docs/prds/memory-engine-unification.md §3 and §4's specific webhook
// requirement: internal/channels/webhook/webhook.go sets SenderID to the
// configured route path (RouteConfig.Path), and that must never surface as
// a user identity, however plausible it looks as one.
func TestWebhookUserIdentityNeverReturnsTheRoutePath(t *testing.T) {
	resolver := userIdentityResolver("webhook")
	id, ok := resolver(messaging.Message{SenderID: "/github", Sender: "webhook"})
	if ok || id != "" {
		t.Fatalf("webhook resolver = (%q, %v), want (\"\", false)", id, ok)
	}
}

// TestUserIdentityResolverPerChannel documents the composition's per-channel
// choice: telegram and email carry a native sender id, the dashboard ("web")
// and webhook do not.
func TestUserIdentityResolverPerChannel(t *testing.T) {
	tests := []struct {
		channel  string
		senderID string
		wantOK   bool
	}{
		{"telegram", "12345", true},
		{"email", "person@example.com", true},
		{"web", "anything", false},
		{"webhook", "/github", false},
		{"unknown-future-channel", "anything", false},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			resolver := userIdentityResolver(tt.channel)
			id, ok := resolver(messaging.Message{SenderID: tt.senderID})
			if ok != tt.wantOK {
				t.Fatalf("%s resolver ok = %v, want %v", tt.channel, ok, tt.wantOK)
			}
			if ok && string(id) != tt.senderID {
				t.Fatalf("%s resolver id = %q, want %q", tt.channel, id, tt.senderID)
			}
		})
	}
}

// TestSenderIDIdentityFailsClosedOnEmptySenderID guards §3's "fail closed":
// an empty SenderID (a channel that carries no per-caller id for this
// particular message) must resolve to no identity, never an empty-string
// identity that Subject would treat as present.
func TestSenderIDIdentityFailsClosedOnEmptySenderID(t *testing.T) {
	id, ok := senderIDIdentity(messaging.Message{SenderID: ""})
	if ok || id != "" {
		t.Fatalf("senderIDIdentity(empty) = (%q, %v), want (\"\", false)", id, ok)
	}
}
