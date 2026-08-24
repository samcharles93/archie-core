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
