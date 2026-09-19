package messaging

// MessageType discriminates inbound gateway messages.
type MessageType string

const (
	MsgText     MessageType = "text"
	MsgImage    MessageType = "image"
	MsgAudio    MessageType = "audio"
	MsgVideo    MessageType = "video"
	MsgDocument MessageType = "document"
	MsgLocation MessageType = "location"
	MsgContact  MessageType = "contact"
	MsgSticker  MessageType = "sticker"
	MsgReaction MessageType = "reaction"
	MsgCallback MessageType = "callback"
	MsgEdited   MessageType = "edited"
	MsgDeleted  MessageType = "deleted"
	MsgSystem   MessageType = "system"
)

// IsMedia reports whether the type carries a media attachment.
func (t MessageType) IsMedia() bool {
	switch t {
	case MsgImage, MsgVideo, MsgAudio, MsgDocument, MsgSticker:
		return true
	}
	return false
}

// IsEphemeral reports whether the type is a transient signal.
func (t MessageType) IsEphemeral() bool {
	switch t {
	case MsgReaction, MsgDeleted:
		return true
	}
	return false
}

// IsRich reports whether the type requires a MessageEvent with
// structured fields rather than a plain text fallback.
func (t MessageType) IsRich() bool {
	switch t {
	case MsgText:
		return false
	default:
		return true
	}
}

// MessageEvent is the rich message model for gateway channels.
type MessageEvent struct {
	Type         MessageType       `json:"type"`
	Text         string            `json:"text,omitempty"`
	ID           string            `json:"id,omitempty"`
	ChannelID    string            `json:"channel_id"`
	Platform     string            `json:"platform"`
	SenderID     string            `json:"sender_id"`
	SenderName   string            `json:"sender_name,omitempty"`
	ReplyToID    string            `json:"reply_to_id,omitempty"`
	ThreadID     string            `json:"thread_id,omitempty"`
	Media        []MediaAttachment `json:"media,omitempty"`
	EditedFrom   string            `json:"edited_from,omitempty"`
	Reaction     string            `json:"reaction,omitempty"`
	ReactedToID  string            `json:"reacted_to_id,omitempty"`
	CallbackData string            `json:"callback_data,omitempty"`
	Timestamp    int64             `json:"timestamp,omitempty"`
	Raw          map[string]any    `json:"raw,omitempty"`
}

// FromID returns the sender identifier.
func (e MessageEvent) FromID() string { return e.SenderID }
