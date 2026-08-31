// Package messaging owns canonical Messages, Conversations, branches,
// channel and source correlation, and typed tool-call/tool-result message
// variants.
//
// This package intentionally contains only the vocabulary already settled
// in docs/architecture/migration-decisions.md section 2: the composite
// Conversation identity, immutable MessageIDs, typed tool-call and
// tool-result messages, and branch lineage through ParentConversationID and
// ForkMessageID. Migrating internal/gateway/'s session, compression,
// approval, and branch logic onto these types is deliberately out of scope
// here — see that section for why, and bd for the tracked follow-up work.
package messaging

import "time"

// ConversationID is the composite identity of a Conversation: a channel and
// the channel-native thread within it. Two conversations with the same
// ChannelID but different ThreadID are distinct.
type ConversationID struct {
	ChannelID string
	ThreadID  string
}

// MessageID is the canonical, immutable identifier for a Message. Once
// assigned it never changes, including across branch and fork operations —
// a forked transcript references the original MessageID it branched from
// rather than minting a new one for shared history.
type MessageID string

// Role identifies who or what produced a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// Conversation is a single thread of Messages within a channel, optionally
// forked from another Conversation.
//
// Parent and child transcript isolation is exact: a fork sees the parent's
// history up to ForkMessageID, and appends to its own independent
// continuation. Nothing appended to the child is visible to the parent, and
// nothing appended to the parent after the fork point is visible to the
// child.
type Conversation struct {
	ID ConversationID

	// ParentConversationID is the Conversation this one branched from, and
	// the zero value when this Conversation is not a branch.
	ParentConversationID ConversationID
	// ForkMessageID is the last MessageID visible to this Conversation from
	// its parent's history. Empty when ParentConversationID is empty.
	ForkMessageID MessageID

	CreatedAt time.Time
}

// IsBranch reports whether this Conversation was forked from another.
func (c Conversation) IsBranch() bool {
	return c.ParentConversationID != ConversationID{}
}

// Message is one immutable, persisted turn in a Conversation.
type Message struct {
	ID             MessageID
	ConversationID ConversationID

	// SourceID is the channel-native identifier (e.g. a Telegram
	// message_id) this Message correlates to. Empty for messages with no
	// upstream identity.
	SourceID string

	Role Role
	Text string

	// ToolCall and ToolResult are populated instead of Text for their
	// respective Roles; a Message carries exactly one of Text, ToolCall, or
	// ToolResult content.
	ToolCall   *ToolCall
	ToolResult *ToolResult

	At time.Time
}

// ToolCall is a typed request from the assistant to invoke a tool.
type ToolCall struct {
	CallID    string
	ToolName  string
	Arguments string // JSON-encoded arguments, opaque to this package
}

// ToolResult is a typed response to a prior ToolCall, correlated by CallID.
type ToolResult struct {
	CallID string
	Output string
	Err    string // non-empty when the tool call failed
}
