package archiemessaging

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

type dummyChatContract struct {
	messaging.ChatContract
}

func TestServiceStartsConfiguredChannels(t *testing.T) {
	d := deps{
		Config: ResolvedConfig{
			TelegramToken: "token-123",
			Telegram: config.TelegramConfig{
				TokenEnv:       "TEST_TG_TOKEN",
				AllowedUserIDs: []int64{123},
			},
			Email: config.EmailConfig{
				ListenAddr: "127.0.0.1:0",
			},
			WebhookAddr: "127.0.0.1:0",
		},
		Log:  slog.Default(),
		Chat: &dummyChatContract{},
	}

	srv, err := compose(t.Context(), d)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(srv.channels) != 3 {
		t.Fatalf("len(srv.channels) = %d, want 3", len(srv.channels))
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	srv.Stop()

	select {
	case startErr := <-errCh:
		if startErr != nil {
			t.Fatalf("srv.Start error: %v", startErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("srv.Start did not exit after Stop")
	}
}

// TestComposeRejectsInvalidChannelConfig pins that a configured front-end
// whose own ValidateConfig refuses the config fails composition. Dropping it
// instead would leave the operator with a service that starts cleanly and
// answers nothing on that channel.
func TestComposeRejectsInvalidChannelConfig(t *testing.T) {
	_, err := compose(t.Context(), deps{
		Config: ResolvedConfig{
			Email: config.EmailConfig{ListenAddr: "127.0.0.1:0"},
		},
		Log:  slog.Default(),
		Chat: &dummyChatContract{},
	})
	if err != nil {
		t.Fatalf("compose with a valid email listen addr: %v", err)
	}

	_, err = compose(t.Context(), deps{
		Config: ResolvedConfig{
			TelegramToken: "token-123",
			// Neither token_env nor a token ref: telegram.ValidateConfig refuses.
		},
		Log:  slog.Default(),
		Chat: &dummyChatContract{},
	})
	if err == nil {
		t.Fatal("compose error = nil, want an error: telegram has no configured credential source")
	}
}
