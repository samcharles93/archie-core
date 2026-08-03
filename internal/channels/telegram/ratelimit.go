package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/go-telegram/bot"
)

// maxRetryAfter bounds how long a 429 can park the update loop. Telegram's
// retry_after is normally single-digit seconds; the cap stops a hostile or
// buggy value from stalling the gateway indefinitely.
const maxRetryAfter = 60 * time.Second

// pipelineErrorHandler logs update-loop failures and, on a rate limit, waits
// the interval Telegram asked for.
//
// go-telegram/bot backs off on its own schedule -- 100ms, doubling, capped at
// 5s (get_updates.go incErrTimeout) -- and never reads RetryAfter. Against a
// "retry_after: 5" that means roughly a dozen further requests inside the
// window Telegram told us to stay silent for, which is what escalates a brief
// 429 into sustained 429s, 502s and request timeouts. The library calls this
// handler synchronously from the polling loop before applying its own backoff,
// so blocking here is the supported way to honour the interval without
// patching the dependency.
func (g *Gateway) pipelineErrorHandler(ctx context.Context) func(error) {
	return func(err error) {
		// errors.As unwraps the library's fmt.Errorf("error get updates, %w").
		var tooMany *bot.TooManyRequestsError
		if !errors.As(err, &tooMany) {
			g.log.Error("telegram pipeline error", "error", err)
			return
		}

		wait := time.Duration(tooMany.RetryAfter) * time.Second
		if wait <= 0 {
			wait = time.Second
		}
		wait = min(wait, maxRetryAfter)

		g.log.Warn("telegram rate limited, pausing update loop",
			"retry_after", wait.String(),
			"component", "gateway-telegram")

		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}
