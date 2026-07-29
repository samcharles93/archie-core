package nats

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

// Publish sends a payload to a subject with no reply routing.
func (c *Client) Publish(ctx context.Context, subject string, payload []byte) error {
	return c.publish(ctx, &nats.Msg{Subject: subject, Data: payload})
}

// PublishUnique sends a payload carrying an idempotency key, so republishing
// the same key inside Config.DedupWindow is suppressed by the server.
func (c *Client) PublishUnique(ctx context.Context, subject, idempotencyKey string, payload []byte) error {
	header := nats.Header{}
	header.Set(idempotencyHeader, idempotencyKey)
	return c.publish(ctx, &nats.Msg{Subject: subject, Data: payload, Header: header})
}

// Respond answers a request on an ephemeral reply inbox.
//
// It uses core NATS rather than the JetStream path the other publish methods
// take: the inbox is not a stream subject, so a JetStream publish to it has
// no stream to accept the message and fails.
func (c *Client) Respond(ctx context.Context, replyAddress string, payload []byte) error {
	if replyAddress == "" {
		return fmt.Errorf("%w: nothing to respond to", eventbus.ErrNoReplyAddress)
	}
	conn, err := c.connection()
	if err != nil {
		return fmt.Errorf("respond on %s: %w", replyAddress, err)
	}
	if err := conn.Publish(replyAddress, payload); err != nil {
		return fmt.Errorf("respond on %s: %w", replyAddress, err)
	}
	return nil
}

// PublishRequest sends a payload recording replyTo as the response address.
func (c *Client) PublishRequest(ctx context.Context, subject, replyTo string, payload []byte) error {
	if replyTo == "" {
		return fmt.Errorf("%w: publishing to %s", eventbus.ErrNoReplyAddress, subject)
	}
	header := nats.Header{}
	header.Set(replyHeader, replyTo)
	return c.publish(ctx, &nats.Msg{Subject: subject, Data: payload, Header: header})
}

// publish is the single JetStream publish path, so every caller gets the same
// connection check and error context.
func (c *Client) publish(ctx context.Context, msg *nats.Msg) error {
	if _, err := c.connection(); err != nil {
		return fmt.Errorf("publish to %s: %w", msg.Subject, err)
	}
	if _, err := c.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("publish to %s: %w", msg.Subject, err)
	}
	c.log.Debug("published", "subject", msg.Subject, "bytes", len(msg.Data))
	return nil
}

// Request performs a core-NATS request/reply and returns the response payload.
//
// This exists so callers never need the raw connection: the original API
// exposed Conn() *nats.Conn, which put the SDK type in every caller's
// imports.
func (c *Client) Request(ctx context.Context, subject string, payload []byte) ([]byte, error) {
	conn, err := c.connection()
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", subject, err)
	}
	msg, err := conn.RequestWithContext(ctx, subject, payload)
	if err != nil {
		// Translate the SDK's no-responder error into the contract's, so a
		// caller can retry a not-yet-subscribed worker without importing
		// the NATS SDK to name the condition.
		if errors.Is(err, nats.ErrNoResponders) {
			return nil, fmt.Errorf("request %s: %w", subject, eventbus.ErrNoResponders)
		}
		return nil, fmt.Errorf("request %s: %w", subject, err)
	}
	return msg.Data, nil
}

// replyInbox is a one-shot subscription awaiting a single response.
type replyInbox struct {
	sub *nats.Subscription
}

var _ eventbus.ReplyInbox = replyInbox{}

// Subject is the inbox address to advertise to the responder.
func (r replyInbox) Subject() string { return r.sub.Subject }

// Next blocks until the reply arrives or ctx is done.
func (r replyInbox) Next(ctx context.Context) ([]byte, error) {
	msg, err := r.sub.NextMsgWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("await reply on %s: %w", r.sub.Subject, err)
	}
	return msg.Data, nil
}

// Close unsubscribes the inbox.
func (r replyInbox) Close() error {
	if err := r.sub.Unsubscribe(); err != nil {
		return fmt.Errorf("close reply inbox %s: %w", r.sub.Subject, err)
	}
	return nil
}

// NewReplyInbox creates a unique inbox that auto-unsubscribes after one
// message.
func (c *Client) NewReplyInbox() (eventbus.ReplyInbox, error) {
	conn, err := c.connection()
	if err != nil {
		return nil, fmt.Errorf("create reply inbox: %w", err)
	}
	sub, err := conn.SubscribeSync(conn.NewInbox())
	if err != nil {
		return nil, fmt.Errorf("subscribe reply inbox: %w", err)
	}
	if err := sub.AutoUnsubscribe(1); err != nil {
		_ = sub.Unsubscribe()
		return nil, fmt.Errorf("configure reply inbox %s: %w", sub.Subject, err)
	}
	return replyInbox{sub: sub}, nil
}
