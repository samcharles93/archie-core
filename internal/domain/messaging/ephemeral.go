package messaging

import (
	"context"
	"time"
)

// EphemeralReply is a reply the sender delivers and then retracts once its
// TTL elapses. It is for status and progress placeholders ("thinking…",
// "running tool X…") that are only meaningful while a turn is in flight and
// should not linger in the chat history after they are superseded.
//
// TTL is measured from the moment the send succeeds. A non-positive TTL
// means the reply is not retracted, which keeps a normal message and an
// ephemeral one with no lifetime on the same path.
type EphemeralReply struct {
	Event MessageEvent
	TTL   time.Duration
}

// MessageDeleter is the optional interface a sender implements to remove a
// message it previously sent. It is the delete counterpart of MediaSender,
// discovered the same way: callers read Capabilities().Delete first so a
// platform without delete support is never probed with a doomed call.
//
// DeleteMessage is addressed by the MessageEvent the send returned, so its
// ID must be the platform identifier for the message to remove. A sender
// that cannot name that identifier must not report the Delete capability.
type MessageDeleter interface {
	DeleteMessage(ctx context.Context, event MessageEvent) error
}

// DeleterOf returns sender's MessageDeleter only when sender reports the
// Delete capability. A sender that implements the interface but reports
// Delete:false is treated as unable to delete -- the capability is the
// contract, not the method set -- and one that reports Delete:true without
// implementing the interface is treated the same way, so a mis-reporting
// adapter degrades instead of panicking.
func DeleterOf(sender any) (MessageDeleter, bool) {
	if !CapabilitiesOf(sender).Delete {
		return nil, false
	}
	deleter, ok := sender.(MessageDeleter)
	return deleter, ok
}
