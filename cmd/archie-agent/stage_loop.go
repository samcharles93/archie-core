package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

// stageBus is the messaging this loop needs: pull the next stage request and
// answer it on the requester's inbox.
type stageBus interface {
	Fetch(ctx context.Context) (eventbus.Message, error)
	Respond(ctx context.Context, replyAddress string, payload []byte) error
}

// runMainLoop runs the fetch-handle-ack loop until the context is cancelled.
func runMainLoop(ctx context.Context, bus stageBus, log *slog.Logger) int {
	return runMainLoopWithDelay(ctx, bus, log, time.Sleep)
}

func runMainLoopWithDelay(ctx context.Context, bus stageBus, log *slog.Logger, delay func(time.Duration)) int {
	for {
		if ctx.Err() != nil {
			log.Info("archie-agent shutting down")
			return 0
		}
		msg, err := bus.Fetch(ctx)
		switch {
		case errors.Is(err, eventbus.ErrNoMessage):
			continue
		case err != nil:
			if ctx.Err() != nil {
				return 0
			}
			log.Error("fetch failed", "err", err)
			delay(1 * time.Second)
			continue
		}
		if err := handle(ctx, msg, bus, log); err != nil {
			log.Error("handle failed", "err", err)
			if err := msg.Nak(); err != nil {
				log.Warn("nak failed", "err", err)
			}
			continue
		}
		if err := msg.Ack(); err != nil {
			log.Warn("ack failed", "err", err)
		}
	}
}
