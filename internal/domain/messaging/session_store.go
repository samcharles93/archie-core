package messaging

import (
	"context"
	"time"
)

// SessionSource identifies the platform, bot identity, and conversation a
// gateway session belongs to. It is the composite natural key for session
// lookup.
type SessionSource struct {
	// Platform is the adapter name ("telegram", "discord", "web").
	Platform string `json:"platform"`

	// BotUser is the bot identity serving this session (e.g. "archie",
	// "winter"). Distinct bot identities on the same platform and channel
	// produce distinct sessions.
	BotUser string `json:"bot_user"`

	// ChannelID is the platform conversation identifier (chat ID, channel
	// name, DM thread).
	ChannelID string `json:"channel_id"`

	// ThreadID identifies the topic thread within the conversation, for
	// platforms that support threading (Telegram supergroup topics,
	// Slack threads, Discord forum posts). Empty for flat chats and the
	// General topic. When non-empty, distinct threads within the same
	// channel produce distinct sessions.
	ThreadID string `json:"thread_id,omitempty"`
}

// SessionContext holds the full metadata for an active gateway session.
// The session store is the persistence layer; callers treat SessionContext
// as a value object.
type SessionContext struct {
	// SessionID is the unique session identifier (UUID).
	SessionID string `json:"session_id"`

	// Source identifies the platform, bot, and conversation.
	Source SessionSource `json:"source"`

	// Title is an optional human-readable name for the session, set
	// via /title. Empty until set.
	Title string `json:"title,omitempty"`

	// ParentSessionID is the session this one was branched from, set
	// by /branch or /fork. Empty for root sessions.
	ParentSessionID string `json:"parent_session_id,omitempty"`

	// BranchName is the name given when branching, set via
	// /branch <name>. Empty for root sessions.
	BranchName string `json:"branch_name,omitempty"`

	// CreatedAt is when the session was first established.
	CreatedAt time.Time `json:"created_at"`

	// LastActiveAt is the timestamp of the most recent message or event
	// on this session. Updated by Touch.
	LastActiveAt time.Time `json:"last_active_at"`
}

// SessionStore persists gateway session metadata and conversation history.
// Each session tracks one platform, bot, and channel combination.
//
// Message history is stored and loaded as canonical Message records
// (archie-core-d9dv). Session lifecycle records stay SessionContext:
// a session's UUID key, platform/bot/title/branch metadata, and the fact
// that several sessions share one channel/thread have no lossless home in
// Conversation, whose composite identity is only
// {ChannelID, ThreadID} and whose canonical Agent/user/binding ownership is
// still open (see docs/architecture/migration-decisions.md section 2).
// Conversation-keying the sessions is tracked as follow-up work once that
// ownership settles.
type SessionStore interface {
	SessionLifecycle
	MessageHistory
}

// SessionLifecycle manages session CRUD and listing.
type SessionLifecycle interface {
	// Save inserts or replaces a session.
	Save(ctx context.Context, s SessionContext) error
	// Get returns a session by ID, or nil when it does not exist.
	Get(ctx context.Context, sessionID string) (*SessionContext, error)
	// GetByChannel returns matching sessions newest first.
	GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error)
	// Delete removes a session. Deleting an absent session is a no-op.
	Delete(ctx context.Context, sessionID string) error
	// Touch marks a session active at the current time.
	Touch(ctx context.Context, sessionID string) error
	// List returns all sessions newest first.
	List(ctx context.Context) ([]SessionContext, error)
}

// MessageHistory manages conversation messages within a session. Messages
// are canonical Message records, the same type channel adapters hand the
// Router, so nothing converts on the way in or out.
type MessageHistory interface {
	// SaveMessage appends one message with a strictly increasing timestamp.
	SaveMessage(ctx context.Context, sessionID string, msg Message) error
	// RecentMessages returns the most recent messages in chronological order.
	RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error)
	// DeleteRecentMessages removes up to n messages and returns the count.
	DeleteRecentMessages(ctx context.Context, sessionID string, n int) (deleted int, err error)
	// MessageCount returns the number of stored messages in a session.
	MessageCount(ctx context.Context, sessionID string) (int, error)
	// SaveMessages appends messages used to inherit branch history.
	SaveMessages(ctx context.Context, sessionID string, msgs []Message) error
	// ReplaceMessages writes replacements before deleting superseded messages.
	// Records absent from both inputs remain untouched.
	ReplaceMessages(ctx context.Context, sessionID string, msgs []Message, superseded []string) error
	// SearchMessages searches the session's full history.
	SearchMessages(ctx context.Context, sessionID string, q MessageQuery) (MessagePage, error)
	// Close releases the database.
	Close() error
}

// MessageQuery is one page request against a session's message history. Limit
// is clamped to MaxMessagePageSize and Offset is never negative.
type MessageQuery struct {
	Query string
	Limit int
	// Offset is the number of ranked matches to skip.
	Offset int
}

// MessagePage is one page of search results.
type MessagePage struct {
	// Messages are ordered by relevance.
	Messages []Message
	// NextOffset is meaningful only when HasMore is true.
	NextOffset int
	HasMore    bool
	// Truncated is retained in the transport contract and is always false for
	// SQLite because it can count the complete result set.
	Truncated bool
}

// Page-size bounds for MessageQuery.
const (
	DefaultMessagePageSize     = 50
	MaxMessagePageSize         = 500
	MaxMessageSearchQueryBytes = 4096
	MaxMessageSearchQueryTerms = 64
)

// RoleForSender classifies a message sender: a message sent by the
// session's bot is the assistant's, anything else is the user's.
func RoleForSender(sender, botUser string) Role {
	if sender == botUser {
		return RoleAssistant
	}
	return RoleUser
}
