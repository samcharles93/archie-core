package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
)

// Run runs the standalone Messaging Service process until ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	log := slog.Default().With("service", "archie-messaging")

	cfg, err := Resolve(o, log)
	if err != nil {
		return fmt.Errorf("resolve messaging options: %w", err)
	}

	chat, closeGateway, err := gatewayrpc.Dial(cfg.Options.Gateway.Target, cfg.Options.Gateway.Token)
	if err != nil {
		return fmt.Errorf("dial gateway (%s): %w", cfg.Options.Gateway.Target, err)
	}
	defer closeGateway()

	health := newReadinessRegistry(cfg.Options, chat)

	srv := compose(deps{
		Config: cfg,
		Log:    log,
		Chat:   chat,
		Health: health,
	})

	return srv.Start(ctx)
}
