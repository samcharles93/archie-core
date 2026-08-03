package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

// TestPipelineErrorHandlerHonoursRetryAfter pins the fix for the carina
// incident: go-telegram/bot backs off 100ms and doubles, ignoring the
// retry_after Telegram sends, so a 429 was answered with a burst of further
// requests inside the window we were told to stay quiet for.
func TestPipelineErrorHandlerHonoursRetryAfter(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		minWait time.Duration
		maxWait time.Duration
	}{
		{
			name:    "bare rate limit error waits the stated interval",
			err:     &bot.TooManyRequestsError{RetryAfter: 1},
			minWait: time.Second,
			maxWait: 3 * time.Second,
		},
		{
			// The library wraps it: fmt.Errorf("error get updates, %w", err).
			name:    "wrapped rate limit error is still recognised",
			err:     fmt.Errorf("error get updates, %w", &bot.TooManyRequestsError{RetryAfter: 1}),
			minWait: time.Second,
			maxWait: 3 * time.Second,
		},
		{
			name:    "zero retry_after still pauses rather than spinning",
			err:     &bot.TooManyRequestsError{RetryAfter: 0},
			minWait: time.Second,
			maxWait: 3 * time.Second,
		},
		{
			name:    "unrelated errors are logged without pausing",
			err:     errors.New("502 Bad Gateway"),
			minWait: 0,
			maxWait: 250 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &Gateway{log: slog.New(slog.DiscardHandler)}
			handle := g.pipelineErrorHandler(context.Background())

			start := time.Now()
			handle(tc.err)
			elapsed := time.Since(start)

			if elapsed < tc.minWait {
				t.Errorf("returned after %s, want at least %s", elapsed, tc.minWait)
			}
			if elapsed > tc.maxWait {
				t.Errorf("returned after %s, want at most %s", elapsed, tc.maxWait)
			}
		})
	}
}

// TestPipelineErrorHandlerReleasesOnShutdown pins that a rate-limit pause does
// not hold the update loop open past cancellation -- otherwise a /restart or
// SIGTERM during a 429 would block for the full retry_after.
func TestPipelineErrorHandlerReleasesOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &Gateway{log: slog.New(slog.DiscardHandler)}
	handle := g.pipelineErrorHandler(ctx)

	cancel()
	start := time.Now()
	handle(&bot.TooManyRequestsError{RetryAfter: 30})

	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("blocked %s after cancellation, want immediate return", elapsed)
	}
}

// TestRetryAfterIsBounded pins the ceiling on how long a single 429 can park
// the loop, so an implausible retry_after cannot stall the gateway.
func TestRetryAfterIsBounded(t *testing.T) {
	if maxRetryAfter > 5*time.Minute {
		t.Errorf("maxRetryAfter = %s, too long to leave the gateway unresponsive", maxRetryAfter)
	}
}
