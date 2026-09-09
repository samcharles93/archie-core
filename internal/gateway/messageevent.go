package gateway

// MessageEvent is the rich message model for all gateway channels. It
// carries the platform detail an adapter needs beyond the persisted
// record, and keeps the Text() and FromID() helpers for callers that only
// want a sender and a body.
//
// Every field except Type is optional. A minimal text message needs
// only Type=MsgText, Text, ChannelID, Platform, and SenderID.
type MessageEvent struct {
	// Type discriminates the message kind.
	Type MessageType `json:"type"`

	// Text is the human-readable message body. It carries the caption
	// for media messages and the full content for text and system
	// messages.
	Text string `json:"text,omitempty"`

	// ── Identifiers ──────────────────────────────────────────────

	// ID is the platform-assigned message identifier. Used for reply
	// threading, reactions, and edit/delete tracking.
	ID string `json:"id,omitempty"`

	// ChannelID identifies the conversation within the platform
	// (chat ID, channel name, DM thread).
	ChannelID string `json:"channel_id"`

	// Platform names the source adapter ("telegram", "discord",
	// "web", "slack").
	Platform string `json:"platform"`

	// ── Sender ───────────────────────────────────────────────────

	// SenderID is the platform-specific sender identifier.
	SenderID string `json:"sender_id"`

	// SenderName is the display name when available.
	SenderName string `json:"sender_name,omitempty"`

	// ── Threading ────────────────────────────────────────────────

	// ReplyToID is the platform message ID this message is in reply
	// to. Empty for top-level messages.
	ReplyToID string `json:"reply_to_id,omitempty"`

	// ThreadID identifies the conversation thread for platforms that
	// support threading (Slack, Discord forums).
	ThreadID string `json:"thread_id,omitempty"`

	// ── Media ────────────────────────────────────────────────────

	// Media holds attached files. Nil for text-only messages.
	Media []MediaAttachment `json:"media,omitempty"`

	// ── Edit tracking ────────────────────────────────────────────

	// EditedFrom is the original text before the edit. Set only when
	// Type is MsgEdited.
	EditedFrom string `json:"edited_from,omitempty"`

	// ── Reactions ────────────────────────────────────────────────

	// Reaction is the emoji used when Type is MsgReaction.
	Reaction string `json:"reaction,omitempty"`

	// ReactedToID is the message being reacted to.
	ReactedToID string `json:"reacted_to_id,omitempty"`

	// ── Callbacks ────────────────────────────────────────────────

	// CallbackData is the payload from a button or inline-keyboard
	// press (Type == MsgCallback).
	CallbackData string `json:"callback_data,omitempty"`

	// ── Timestamp ────────────────────────────────────────────────

	// Timestamp is the platform-assigned message time in Unix
	// milliseconds. Zero when unknown.
	Timestamp int64 `json:"timestamp,omitempty"`

	// ── Platform payload ─────────────────────────────────────────

	// Raw is the original platform-specific payload. Adapters set
	// this for debug logging or handlers that need platform details
	// not captured by the generic fields.
	Raw map[string]any `json:"raw,omitempty"`
}

// FromID returns the sender identifier, under the name callers that only
// need a sender use.
func (e MessageEvent) FromID() string { return e.SenderID }
