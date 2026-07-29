package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

// Fetch returns the next message from the pull consumer.
//
// It reports eventbus.ErrNoMessage when the poll timeout expires with nothing
// queued. The original API returned (nil, nil) in that case, which every
// caller had to remember to distinguish from a real message.
func (c *Client) Fetch(ctx context.Context) (eventbus.Message, error) {
	if _, err := c.connection(); err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	batch, err := c.consumer.Fetch(1, jetstream.FetchMaxWait(c.cfg.PollTimeout))
	if err != nil {
		return nil, fmt.Errorf("fetch from %s: %w", c.cfg.ConsumerName, err)
	}

	for msg := range batch.Messages() {
		if msg == nil {
			continue
		}
		// Honour cancellation: return the message unacked so JetStream
		// redelivers it rather than losing it to a shutting-down daemon.
		if err := ctx.Err(); err != nil {
			_ = msg.Nak()
			return nil, fmt.Errorf("fetch from %s: %w", c.cfg.ConsumerName, err)
		}
		return message{msg: msg}, nil
	}

	if err := batch.Error(); err != nil {
		return nil, fmt.Errorf("fetch batch from %s: %w", c.cfg.ConsumerName, err)
	}
	return nil, eventbus.ErrNoMessage
}

// Subscribe delivers messages matching filterSubject to handle until ctx is
// done. It exists so callers do not need the SDK's consume machinery; handle
// receives the same boundary type as Fetch.
func (c *Client) Subscribe(ctx context.Context, filterSubject string, handle func(eventbus.Message) error) error {
	if _, err := c.connection(); err != nil {
		return fmt.Errorf("subscribe %s: %w", filterSubject, err)
	}

	consumer, err := c.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: filterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       c.cfg.AckWait,
		MaxDeliver:    c.cfg.MaxDeliver,
	})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", filterSubject, err)
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		if err := handle(message{msg: msg}); err != nil {
			c.log.Error("message handler failed",
				"subject", msg.Subject(), "error", err)
			_ = msg.Nak()
			return
		}
		if err := msg.Ack(); err != nil {
			c.log.Error("ack failed", "subject", msg.Subject(), "error", err)
		}
	})
	if err != nil {
		return fmt.Errorf("consume %s: %w", filterSubject, err)
	}
	defer consumeCtx.Stop()

	<-ctx.Done()
	return nil
}
