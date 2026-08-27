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

// TestPipelineErrorHandlerDoesNotBlock pins that the handler is a pure logger
// post go-telegram/bot v1.24.0: the library now honours retry_after itself
// (https://github.com/go-telegram/bot/releases/tag/v1.24.0), so the handler
// that used to block for the stated interval on our side would double-wait.
func TestPipelineErrorHandlerDoesNotBlock(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "bare rate limit error",
			err:  &bot.TooManyRequestsError{RetryAfter: 30},
		},
		{
			// The library wraps it: fmt.Errorf("error get updates, %w").
			name: "wrapped rate limit error is still recognised",
			err:  fmt.Errorf("error get updates, %w", &bot.TooManyRequestsError{RetryAfter: 30}),
		},
		{
			name: "unrelated errors are logged without pausing",
			err:  errors.New("502 Bad Gateway"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &Gateway{log: slog.New(slog.DiscardHandler)}
			handle := g.pipelineErrorHandler(context.Background())

			start := time.Now()
			handle(tc.err)
			elapsed := time.Since(start)

			if elapsed > 250*time.Millisecond {
				t.Errorf("returned after %s, want immediate return (no local backoff)", elapsed)
			}
		})
	}
}
