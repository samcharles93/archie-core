package gateway

// MediaAttachment describes a file attached to a message. Platform-agnostic;
// platform-specific fields live in Raw.
//
// Numeric fields use pointers so that zero is distinguishable from
// "unknown" across a JSON round-trip: a 0-byte file is valid and
// different from a file whose size was never set.
type MediaAttachment struct {
	// Type is "image", "video", "audio", or "document".
	Type string `json:"type"`

	// FileID is the platform-assigned identifier for download APIs.
	FileID string `json:"file_id"`

	// URL is a direct download URL when available. May be empty for
	// platforms that require a download API call.
	URL string `json:"url,omitempty"`

	// Path is a local filesystem path on the host that produced the
	// attachment. It is the alternative to URL for outbound delivery: a
	// URL is fetched by the platform, a Path is uploaded by us. A file the
	// agent wrote has no URL and never gets one -- publishing it would
	// trade a delivery problem for a hosting and access-control one -- so
	// senders that support upload must read this.
	//
	// Exactly one of URL and Path is meaningful for an outbound send.
	Path string `json:"path,omitempty"`

	// MIMEType is the content type (e.g. "image/png", "audio/ogg").
	MIMEType string `json:"mime_type,omitempty"`

	// FileName is the original file name when known.
	FileName string `json:"file_name,omitempty"`

	// FileSize is the attachment size in bytes. nil means unknown.
	FileSize *int64 `json:"file_size,omitempty"`

	// Width is the pixel width for images and video. nil means unknown
	// or not applicable.
	Width *int `json:"width,omitempty"`

	// Height is the pixel height for images and video.
	Height *int `json:"height,omitempty"`

	// Duration is the play time in seconds for audio and video.
	Duration *int `json:"duration,omitempty"`
}

// MessageEvent is the rich message model for all gateway channels. It
// extends the 3-field Message struct for new adapters while
// remaining backward-compatible via the Text() and FromID() helpers.
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

// FromID returns the sender identifier. Backward-compatible alias for
// code that uses Message.From.
func (e MessageEvent) FromID() string { return e.SenderID }
