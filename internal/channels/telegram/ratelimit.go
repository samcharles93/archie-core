package telegram

import (
	"context"
	"errors"

	"github.com/go-telegram/bot"
)

// pipelineErrorHandler logs update-loop failures.
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
