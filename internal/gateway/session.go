package gateway

import "time"

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
// SessionStore is the persistence layer; callers treat SessionContext as
// a value object.
type SessionContext struct {
	// SessionID is the unique session identifier (UUID).
	SessionID string `json:"session_id"`

	// Source identifies the platform, bot, and conversation.
	Source SessionSource `json:"source"`

	// CreatedAt is when the session was first established.
	CreatedAt time.Time `json:"created_at"`

	// LastActiveAt is the timestamp of the most recent message or event
	// on this session. Updated by Touch.
	LastActiveAt time.Time `json:"last_active_at"`
}
