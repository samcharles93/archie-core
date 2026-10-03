package messaging

import (
	"context"
	"time"
)

// EphemeralReply is a reply deleted after TTL from a successful send. A
// non-positive TTL is never deleted.
type EphemeralReply struct {
	Event MessageEvent
	TTL   time.Duration
}

// MessageDeleter deletes a message the sender sent, addressed by the
// MessageEvent the send returned.
type MessageDeleter interface {
	DeleteMessage(ctx context.Context, event MessageEvent) error
}

// DeleterOf returns sender's MessageDeleter when it reports the Delete
// capability and implements the interface.
func DeleterOf(sender any) (MessageDeleter, bool) {
	if !CapabilitiesOf(sender).Delete {
		return nil, false
	}
	deleter, ok := sender.(MessageDeleter)
	return deleter, ok
}
