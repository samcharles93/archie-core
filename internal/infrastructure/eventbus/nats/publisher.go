package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
)

// PublishTask publishes a discovered issue to the subject its labels select.
// The dedup header suppresses republishing the same issue inside the
// configured dedup window.
func (c *Client) PublishTask(ctx context.Context, task TaskEnvelope) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("encode task %s/%s#%d: %w", task.Owner, task.Repo, task.Number, err)
	}

	header := nats.Header{}
	header.Set("Nats-Msg-Id", task.DedupKey())

	subject := task.Subject()
	if err := c.publish(ctx, &nats.Msg{Subject: subject, Data: data, Header: header}); err != nil {
		return err
	}

	c.log.Debug("published task",
		"subject", subject,
		"owner", task.Owner,
		"repo", task.Repo,
		"number", task.Number,
		"identity", task.Identity)
	return nil
}

// PublishRequest publishes a stage execution request, recording replyTo in the
// reply header so the worker knows where to answer. JetStream consumes a
// message's Reply field for its own PubAck, hence the header.
func (c *Client) PublishRequest(ctx context.Context, subject, replyTo string, payload []byte) error {
	if replyTo == "" {
		return fmt.Errorf("%w: publishing %s", ErrNoReplyAddress, subject)
	}
	header := nats.Header{}
	header.Set(ReplyHeader, replyTo)
	return c.publish(ctx, &nats.Msg{Subject: subject, Data: payload, Header: header})
}

// Publish sends a payload to a subject with no reply routing.
func (c *Client) Publish(ctx context.Context, subject string, payload []byte) error {
	return c.publish(ctx, &nats.Msg{Subject: subject, Data: payload})
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
	return nil
}

// Request performs a core-NATS request/reply and returns the response payload.
//
// This exists so callers never need the raw connection: the previous API
// exposed Conn() *nats.Conn, which put the SDK type in every caller's imports.
// nats.ErrNoResponders is returned unwrapped enough for errors.Is to match, so
// callers can still retry while a responder starts up.
func (c *Client) Request(ctx context.Context, subject string, payload []byte) ([]byte, error) {
	conn, err := c.connection()
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", subject, err)
	}
	msg, err := conn.RequestWithContext(ctx, subject, payload)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", subject, err)
	}
	return msg.Data, nil
}

// ReplyInbox is a one-shot subscription awaiting a single response.
type ReplyInbox struct {
	sub *nats.Subscription
}

// Subject is the inbox address to advertise to the responder.
func (r ReplyInbox) Subject() string { return r.sub.Subject }

// Next blocks until the reply arrives or ctx is done.
func (r ReplyInbox) Next(ctx context.Context) ([]byte, error) {
	msg, err := r.sub.NextMsgWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("await reply on %s: %w", r.sub.Subject, err)
	}
	return msg.Data, nil
}

// Close unsubscribes the inbox.
func (r ReplyInbox) Close() error {
	if err := r.sub.Unsubscribe(); err != nil {
		return fmt.Errorf("close reply inbox %s: %w", r.sub.Subject, err)
	}
	return nil
}

// NewReplyInbox creates a unique inbox that auto-unsubscribes after one
// message.
func (c *Client) NewReplyInbox() (ReplyInbox, error) {
	conn, err := c.connection()
	if err != nil {
		return ReplyInbox{}, fmt.Errorf("create reply inbox: %w", err)
	}
	sub, err := conn.SubscribeSync(conn.NewInbox())
	if err != nil {
		return ReplyInbox{}, fmt.Errorf("subscribe reply inbox: %w", err)
	}
	if err := sub.AutoUnsubscribe(1); err != nil {
		_ = sub.Unsubscribe()
		return ReplyInbox{}, fmt.Errorf("configure reply inbox %s: %w", sub.Subject, err)
	}
	return ReplyInbox{sub: sub}, nil
}
