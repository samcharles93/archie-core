package eventbus

import (
	"context"
	"errors"
)

// Sentinel outcomes callers match with errors.Is. These are bus semantics,
// not broker details, so they belong in the contract: an implementation that
// cannot express them cannot satisfy it.
var (
	// ErrNoMessage reports that a fetch completed normally with nothing
	// queued. Polling an idle queue is an expected outcome, not a failure.
	ErrNoMessage = errors.New("eventbus: no message available")

	// ErrNotConnected reports use of a bus whose connection is closed or was
	// never established.
	ErrNotConnected = errors.New("eventbus: not connected")

	// ErrNoResponders reports a request sent to a subject nobody is
	// listening on. It is usually transient -- a worker that has connected
	// but not yet subscribed -- so callers typically retry within a deadline
	// rather than failing immediately.
	ErrNoResponders = errors.New("eventbus: no responders")
)

// Message is one delivered message. The payload is opaque: decoding it is the
// owning domain's job.
type Message interface {
	// Data returns the raw payload.
	Data() []byte

	// Subject returns the address the message arrived on.
	Subject() string

	// Ack marks the message handled so it is not redelivered.
	Ack() error

	// Nak returns the message for redelivery.
	Nak() error
}

// Publisher sends messages.
type Publisher interface {
	// Publish sends payload to subject.
	Publish(ctx context.Context, subject string, payload []byte) error

	// PublishUnique sends payload to subject carrying an idempotency key.
	// Republishing the same key within the implementation's deduplication
	// window is suppressed.
	//
	// This is a required semantic rather than a broker detail: work intake
	// rediscovers the same issue on every poll, so at-least-once delivery
	// without deduplication would enqueue it repeatedly.
	PublishUnique(ctx context.Context, subject, idempotencyKey string, payload []byte) error
}

// Consumer receives messages.
type Consumer interface {
	// Fetch returns the next message, or ErrNoMessage if none arrives before
	// the implementation's poll deadline.
	Fetch(ctx context.Context) (Message, error)

	// Subscribe delivers messages matching filterSubject to handle until ctx
	// is done. A handler returning an error causes redelivery.
	Subscribe(ctx context.Context, filterSubject string, handle func(Message) error) error
}

// Requester performs request/reply.
type Requester interface {
	// Request sends payload to subject and waits for a single response.
	Request(ctx context.Context, subject string, payload []byte) ([]byte, error)
}

// Bus is the full contract. Depend on the narrowest interface that fits:
// a publisher-only caller should accept Publisher, not Bus.
type Bus interface {
	Publisher
	Consumer
	Requester
}
