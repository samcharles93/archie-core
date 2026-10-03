// Package messaging defines messages, conversations and the chat contracts
// channels and the gateway share.
package messaging

import "time"

// ConversationID is the composite identity of a Conversation: a channel and
// the channel-native thread within it. Two conversations with the same
// ChannelID but different ThreadID are distinct.
type ConversationID struct {
	ChannelID string
	ThreadID  string
}

func (c ConversationID) String() string {
	if c.ThreadID == "" {
		return c.ChannelID
	}
	return c.ChannelID + "/" + c.ThreadID
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

// Conversation is a thread of Messages in a channel, optionally forked from
// another at ForkMessageID. Fork and parent histories are isolated.
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

	// Sender is the channel-native display name of the author. Role is rebuilt
	// from it at the gateway wire boundary.
	Sender string

	// SenderID is the channel-native stable ID of the person who sent the
	// message, or empty when there is none.
	SenderID string

	Role Role
	Text string

	// Media is the message's attachment metadata, without bytes.
	Media []MediaAttachment `json:"media,omitempty"`

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
