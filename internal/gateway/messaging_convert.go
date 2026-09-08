package gateway

import (
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// This file is the single boundary between the gateway's channel-facing
// Message and the canonical messaging.Message persisted by the session
// store.
//
// CURRENT: gateway.Message is built by channel adapters (Telegram, email,
// webhook, web UI) and carries transport-only routing context (ChannelID,
// ThreadID, Page) alongside the persisted fields. SessionStore persists the
// stored subset; see scanMessages, which leaves the transport fields empty
// on read.
//
// TARGET (migration-decisions.md section 2): messaging.Message is the
// canonical persisted record. The store speaks it; channel adapters are
// migrated onto it one by one (bd label messaging-migration), after which
// gateway.Message is deleted. Until then, every conversion goes through
// these two functions -- no caller maps the fields by hand.
//
// Role derivation matches the comparisons this package already performs
// (compressTurnHistory, messagesToCompressed, PriorReply, and the
// sessioncurator adapter): a message sent by the session's bot identity is
// the assistant's, anything else is the user's.

// ToStoredMessage converts a channel-facing message into its canonical
// stored form. botUser is the session's bot identity. The message's
// channel/thread address its conversation; Page is transport-only and does
// not enter the record.
func ToStoredMessage(msg Message, botUser string) messaging.Message {
	role := messaging.RoleUser
	if msg.From == botUser {
		role = messaging.RoleAssistant
	}
	return messaging.Message{
		ID:             messaging.MessageID(msg.MessageID),
		ConversationID: messaging.ConversationID{ChannelID: msg.ChannelID, ThreadID: msg.ThreadID},
		SourceID:       msg.SourceID,
		Sender:         msg.From,
		Role:           role,
		Text:           msg.Text,
		At:             msg.At,
	}
}

// FromStoredMessage converts a canonical stored record back to the gateway
// view. The result carries exactly the fields the store persists today:
// transport-only fields stay empty, as scanMessages leaves them.
func FromStoredMessage(stored messaging.Message) Message {
	return Message{
		MessageID: string(stored.ID),
		SourceID:  stored.SourceID,
		From:      stored.Sender,
		Text:      stored.Text,
		At:        stored.At,
	}
}
