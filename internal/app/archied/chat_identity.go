package archied

import (
	"github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// carriesPersonIdentity reports whether a platform's SenderID names a person,
// per docs/prds/memory-engine-unification.md §3: resolution is supplied per
// channel by the composition, never inferred by the engine or guessed from a
// channel-neutral "channel:SenderID" concatenation.
//
// Telegram and email carry a native per-person sender id (a Telegram numeric
// user id, an SMTP from address). The dashboard ("web") has one bearer token,
// not users, and a webhook carries no per-caller identity at all: its route path
// is a source, not a person, and never enters SenderID
// (internal/channels/webhook/webhook.go -- it travels in the transport-only
// Inbound.BudgetKey instead). Treating a URL or a shared token as an identity
// would give it the same standing as a real user, which the read path's
// isolation guarantee depends on never happening.
//
// An unknown platform fails closed, so a channel added later gets global and
// agent scopes only until someone decides deliberately that it carries people.
func carriesPersonIdentity(platform string) bool {
	switch platform {
	case "telegram", "email":
		return true
	default:
		return false
	}
}

// userIdentityResolver is the composition's identity policy for a turn, keyed on
// the platform the message itself names rather than on the process that serves
// it.
//
// It was closed over a single channel name, and one Gateway Router serves every
// channel, so that name was "web" even for a Telegram turn: the allowlist above
// was real code no production call site reached with a real channel name, and
// Subject.UserID was always empty (archie-core-c1qx). Reading the platform off
// the inbound is what makes the policy reachable.
func userIdentityResolver() func(messaging.Inbound) (memory.IdentityID, bool) {
	return func(in messaging.Inbound) (memory.IdentityID, bool) {
		if !carriesPersonIdentity(in.Platform) || in.Message.SenderID == "" {
			return "", false
		}
		return memory.IdentityID(in.Message.SenderID), true
	}
}
