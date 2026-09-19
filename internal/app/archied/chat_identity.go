package archied

import (
	"github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// userIdentityResolver returns the gateway.TurnRunnerConfig.UserIdentity
// function for one channel, per docs/prds/memory-engine-unification.md §3:
// resolution is supplied per channel by the composition, never inferred by
// the engine or guessed from a channel-neutral "channel:SenderID"
// concatenation.
//
// Telegram and email carry a native per-person sender id (a Telegram numeric
// user id, an SMTP from address). The dashboard ("web") has one bearer
// token, not users, and a webhook's SenderID is the configured route path
// (internal/channels/webhook/webhook.go), not a person -- treating either as
// an identity would give a URL or a shared token the same standing as a
// real user, which the read path's isolation guarantee depends on never
// happening. Both resolve nothing, so a turn on either channel gets global
// and agent scopes only (Subject.WritableScopes' fail-closed behaviour).
func userIdentityResolver(channel string) func(messaging.Message) (memory.IdentityID, bool) {
	switch channel {
	case "telegram", "email":
		return senderIDIdentity
	default:
		return noUserIdentity
	}
}

// senderIDIdentity resolves a channel's own SenderID as the user's identity.
// Empty means the channel carries no per-person identity for this message.
func senderIDIdentity(msg messaging.Message) (memory.IdentityID, bool) {
	if msg.SenderID == "" {
		return "", false
	}
	return memory.IdentityID(msg.SenderID), true
}

// noUserIdentity resolves nothing: the channel it is wired to (the
// dashboard, a webhook) carries no per-person identity at all.
func noUserIdentity(messaging.Message) (memory.IdentityID, bool) {
	return "", false
}
