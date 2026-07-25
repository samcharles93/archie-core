package gateway

// MessageType discriminates inbound gateway messages. Every MessageEvent
// carries a type so handlers can branch on message kind without inspecting
// the raw platform payload.
type MessageType string

const (
	// MsgText is a plain text message.
	MsgText MessageType = "text"

	// MsgImage is a message with an image attachment.
	MsgImage MessageType = "image"

	// MsgVideo is a message with a video attachment.
	MsgVideo MessageType = "video"

	// MsgAudio is a message with an audio attachment (voice note, etc.).
	MsgAudio MessageType = "audio"

	// MsgDocument is a message with a file attachment.
	MsgDocument MessageType = "document"

	// MsgLocation is a shared location.
	MsgLocation MessageType = "location"

	// MsgSticker is a sticker or animated emoji.
	MsgSticker MessageType = "sticker"

	// MsgContact is a shared contact card.
	MsgContact MessageType = "contact"

	// MsgCallback is a button press or inline keyboard interaction.
	MsgCallback MessageType = "callback"

	// MsgReaction is an emoji reaction on a prior message.
	MsgReaction MessageType = "reaction"

	// MsgEdited signals that a prior message text was changed.
	MsgEdited MessageType = "edited"

	// MsgDeleted signals that a prior message was removed.
	MsgDeleted MessageType = "deleted"

	// MsgSystem is a non-user system event (member join/leave, etc.).
	MsgSystem MessageType = "system"
)

// IsMedia reports whether the type carries a media attachment.
func (t MessageType) IsMedia() bool {
	switch t {
	case MsgImage, MsgVideo, MsgAudio, MsgDocument, MsgSticker:
		return true
	}
	return false
}

// IsEphemeral reports whether the type is a transient signal
// (typing indicators, read receipts) that should not be stored.
// Note: MsgEdited is NOT ephemeral  --  it carries content (original
// and updated text) that must be persisted to avoid serving stale data.
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
