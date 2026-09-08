package gateway

import (
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// This file holds the one piece of the old channel-facing Message that
// outlived it: the role derivation.
//
// messaging.Message is the canonical persisted record and is now also the
// currency at the channel-facing boundary. Channel adapters build it
// directly and set Role themselves (always messaging.RoleUser -- a channel
// carries only what a person said), and the gateway sets
// messaging.RoleAssistant on the reply it generates, so nothing converts
// between two shapes of a message in process any more.
//
// The wire is the exception. pb.Message carries no role, so both sides of
// the gRPC contract derive it from the owning session's bot identity
// instead of adding a field to the proto -- see
// internal/infrastructure/gatewayrpc. Because both derive from the same
// session, they agree.

// RoleForSender reports the role of a message written by sender in a
// session whose bot identity is botUser. It is the comparison this package
// already makes elsewhere (compressTurnHistory, messagesToCompressed,
// PriorReply, and the sessioncurator adapter): a message sent by the
// session's bot is the assistant's, anything else is the user's.
func RoleForSender(sender, botUser string) messaging.Role {
	if sender == botUser {
		return messaging.RoleAssistant
	}
	return messaging.RoleUser
}
