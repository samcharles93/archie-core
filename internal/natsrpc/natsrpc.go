// Package natsrpc holds the request/reply plumbing shared by
// archie-core's three core-NATS RPC surfaces (storerpc, worktreerpc,
// forgerpc): a timeout-bounded client call, multi-subject server
// registration with rollback on partial failure, and a JSON error
// envelope so handlers don't each reimplement "marshal error, log
// encode/respond failures."
package natsrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// Envelope is the error half of an RPC response. Embed it in a
// response type (it flattens into the same JSON object via
// encoding/json's anonymous-field promotion) to get Err/NewEnvelope for
// free instead of hand-rolling an `Error string` field and its checks.
type Envelope struct {
	Error string `json:"error,omitempty"`
}

// NewEnvelope builds an Envelope from err, leaving Error empty on nil.
func NewEnvelope(err error) Envelope {
	if err == nil {
		return Envelope{}
	}
	return Envelope{Error: err.Error()}
}

// Err converts a populated Error string back into an error, or nil.
func (e Envelope) Err() error {
	if e.Error == "" {
		return nil
	}
	return errors.New(e.Error)
}

// Client sends timeout-bounded NATS request/reply calls.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
}

// Request sends data to subject and returns the reply payload.
func (c *Client) Request(ctx context.Context, subject string, data []byte) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	reply, err := c.Conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", subject, err)
	}
	return reply.Data, nil
}

// Call marshals req, sends it to subject, and unmarshals the reply
// into a Resp. It does not inspect Resp's error envelope  --  callers
// that embed Envelope should check Err() themselves, since natsrpc
// has no way to know a bare Resp type embeds one.
func Call[Resp any](ctx context.Context, c *Client, subject string, req any) (Resp, error) {
	var zero Resp
	data, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("encode %s request: %w", subject, err)
	}
	reply, err := c.Request(ctx, subject, data)
	if err != nil {
		return zero, err
	}
	var resp Resp
	if err := json.Unmarshal(reply, &resp); err != nil {
		return zero, fmt.Errorf("%s: decode response: %w", subject, err)
	}
	return resp, nil
}

// Registration pairs a subject with its handler for RegisterAll.
type Registration struct {
	Subject string
	Handler nats.MsgHandler
}

// RegisterAll subscribes every registration on nc. If any subscribe
// call fails, every subscription made so far is unsubscribed before
// returning the error  --  a server never ends up half-registered. The
// returned func unsubscribes all of them.
func RegisterAll(nc *nats.Conn, regs []Registration) (unsubscribe func(), err error) {
	subs := make([]*nats.Subscription, 0, len(regs))
	for _, r := range regs {
		sub, err := nc.Subscribe(r.Subject, r.Handler)
		if err != nil {
			for _, s := range subs {
				_ = s.Unsubscribe()
			}
			return nil, fmt.Errorf("subscribe %s: %w", r.Subject, err)
		}
		subs = append(subs, sub)
	}
	return func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}, nil
}

// Respond marshals v and replies to msg, logging (rather than
// returning) any encode or transport failure  --  a handler has no
// meaningful way to retry or propagate a respond failure to its caller.
// pkg prefixes the log message (e.g. "storerpc") to identify the
// surface a failure came from.
func Respond(msg *nats.Msg, log *slog.Logger, pkg string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		if log != nil {
			log.Error(pkg+": encode response failed", "err", err)
		}
		return
	}
	if err := msg.Respond(data); err != nil && log != nil {
		log.Error(pkg+": respond failed", "err", err)
	}
}
