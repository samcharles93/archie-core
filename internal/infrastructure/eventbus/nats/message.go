package nats

import (
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

// replyHeader carries the requester's reply inbox.
//
// JetStream consumes a message's Reply field for its own PubAck, so the
// address travels as a header instead. That is a NATS detail: the contract
// exposes it as [eventbus.Message.ReplyAddress] and callers never name the
// header.
const replyHeader = "X-Archie-Reply"

// idempotencyHeader is JetStream's deduplication key. Messages republished
// with the same value inside Config.DedupWindow are suppressed by the server.
const idempotencyHeader = "Nats-Msg-Id"

// message adapts a jetstream.Msg to the broker-neutral contract.
//
// It is unexported: callers receive it as an eventbus.Message, so no consumer
// can reach the SDK type through it. Fetch once returned jetstream.Msg
// directly, which put the NATS SDK in every consumer's imports.
type message struct {
	msg jetstream.Msg
}

var _ eventbus.Message = message{}

// Data returns the raw payload.
func (m message) Data() []byte { return m.msg.Data() }

// Subject returns the subject the message arrived on.
func (m message) Subject() string { return m.msg.Subject() }

// ReplyAddress returns the requester's reply inbox.
func (m message) ReplyAddress() (string, error) {
	if addr := m.msg.Headers().Get(replyHeader); addr != "" {
		return addr, nil
	}
	return "", fmt.Errorf("%w: on %s", eventbus.ErrNoReplyAddress, m.Subject())
}

// Ack marks the message handled so JetStream will not redeliver it.
func (m message) Ack() error {
	if err := m.msg.Ack(); err != nil {
		return fmt.Errorf("ack message on %s: %w", m.Subject(), err)
	}
	return nil
}

// Nak returns the message for redelivery.
func (m message) Nak() error {
	if err := m.msg.Nak(); err != nil {
		return fmt.Errorf("nak message on %s: %w", m.Subject(), err)
	}
	return nil
}
