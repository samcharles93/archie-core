package archieui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"

	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/webui"
)

// Run serves the dashboard until ctx is cancelled. It resolves its own
// options, dials the Gateway and State Store contracts itself, composes the
// HTTP surface over them, and unwinds listener then clients in reverse order
// on shutdown.
func Run(ctx context.Context, options Options) error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("component", "ui")
	opts, err := Resolve(options, log)
	if err != nil {
		return err
	}

	var cleanups []func()
	defer func() {
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
	}()

	tasks, closeState, err := staterpc.Dial(opts.State.Target, opts.State.Token)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, closeState)
	chat, closeGateway, err := gatewayrpc.Dial(opts.Gateway.Target)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, closeGateway)

	srv := compose(deps{
		Options: opts,
		Log:     log,
		Store:   tasks,
		Chat:    chat,
		Health:  newReadinessRegistry(opts, tasks, chat),
	})

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return fmt.Errorf("listen for ui: %w", err)
	}
	cleanups = append(cleanups, func() { _ = listener.Close() })
	log.Info("archie-ui running",
		"addr", listener.Addr().String(),
		"open", webui.DashboardURL(listener.Addr().String(), opts.Token),
		"gateway", opts.Gateway.Target,
		"state", opts.State.Target,
	)

	return serve(ctx, listener, srv.Handler(), opts)
}

// serve runs handler on listener until ctx ends, then shuts the HTTP server
// down within ShutdownTimeout so in-flight dashboard requests finish before
// the contract clients close.
func serve(ctx context.Context, listener net.Listener, handler http.Handler, opts Options) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: opts.ReadHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
