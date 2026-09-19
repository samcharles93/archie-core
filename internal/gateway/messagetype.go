package gateway

import "github.com/samcharles93/archie-core/internal/domain/messaging"

// MessageType discriminates inbound gateway messages.
type MessageType = messaging.MessageType

const (
	MsgText     = messaging.MsgText
	MsgImage    = messaging.MsgImage
	MsgVideo    = messaging.MsgVideo
	MsgAudio    = messaging.MsgAudio
	MsgDocument = messaging.MsgDocument
	MsgLocation = messaging.MsgLocation
	MsgSticker  = messaging.MsgSticker
	MsgContact  = messaging.MsgContact
	MsgCallback = messaging.MsgCallback
	MsgReaction = messaging.MsgReaction
	MsgEdited   = messaging.MsgEdited
	MsgDeleted  = messaging.MsgDeleted
	MsgSystem   = messaging.MsgSystem
)
