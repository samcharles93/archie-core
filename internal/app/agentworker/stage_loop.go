package agentworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/eventbus"
)

// stageBus is the typed single-stage delivery capability required by the loop.
type stageBus interface {
	agentexec.StageConsumer
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
		message, err := bus.FetchStage(ctx)
		switch {
		case errors.Is(err, eventbus.ErrNoMessage):
			continue
		case err != nil:
			if ctx.Err() != nil {
				return 0
			}
			log.Error("fetch failed", "err", err)
			delay(time.Second)
			continue
		}
		if err := handleSingleStageRequest(ctx, message, log, productionSingleStageDependencies()); err != nil {
			log.Error("handle failed", "err", err)
			if err := message.Nak(); err != nil {
				log.Warn("nak failed", "err", err)
			}
			// A Nak requests redelivery. Without a backoff, a deterministic
			// failure (a broken gate, a malformed request) refetches and
			// fails again immediately, flooding the log with one line per
			// loop iteration instead of one per genuine retry.
			delay(time.Second)
			continue
		}
		if err := message.Ack(); err != nil {
			log.Warn("ack failed", "err", err)
		}
	}
}
