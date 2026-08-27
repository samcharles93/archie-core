package telegram

import (
	"context"
	"errors"

	"github.com/go-telegram/bot"
)

// pipelineErrorHandler logs update-loop failures.
//
// go-telegram/bot v1.24.0 fixed getUpdates to honour retry_after on a 429
// itself (https://github.com/go-telegram/bot/releases/tag/v1.24.0), instead
// of retrying on its own 100ms-doubling-to-5s schedule regardless of what
// Telegram asked for. This handler used to block for RetryAfter itself to
// compensate; that workaround is gone now that the library does it.
func (g *Gateway) pipelineErrorHandler(_ context.Context) func(error) {
	return func(err error) {
		if tooMany, ok := errors.AsType[*bot.TooManyRequestsError](err); ok {
			g.log.Warn("telegram rate limited", "retry_after", tooMany.RetryAfter,
				"component", "gateway-telegram")
			return
		}
		g.log.Error("telegram pipeline error", "error", err)
	}
}
