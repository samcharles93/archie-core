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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/domain/access"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/webui"
)

// buildAccessChain loads the stored policies over the wire and builds the
// engine. The wire client satisfies the two access contracts the dashboard
// needs alongside the engine.
// buildAccessChain loads the stored policies over the wire and builds the
// engine. A State Store serving no policies has no chain to evaluate: the
// dashboard degrades to the credential check rather than refusing to serve,
// so an unavailable policy store is a warning, not a boot failure.
func buildAccessChain(ctx context.Context, store *staterpc.Client, log *slog.Logger) (access.Authorizer, []infraaccess.Problem, error) {
	stored, err := store.ListPolicies(ctx)
	if err != nil {
		if status.Code(err) == codes.Unavailable {
			log.Warn("access chain unavailable; the credential check is the gate", "err", err)
			return nil, nil, nil
		}
		return nil, nil, err
	}
	engine, err := infraaccess.New(stored)
	if err != nil {
		return nil, nil, err
	}
	for _, problem := range engine.Problems() {
		log.Error("stored access policy is invalid and denies its level",
			"policy", problem.Policy.ID, "level", problem.Policy.Level, "err", problem.Err)
	}
	return engine, engine.Problems(), nil
}

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
	chat, closeGateway, err := gatewayrpc.Dial(opts.Gateway.Target, opts.Gateway.Token)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, closeGateway)

	chain, problems, err := buildAccessChain(ctx, tasks, log)
	if err != nil {
		return err
	}
	for _, problem := range problems {
		log.Error("stored access policy is invalid and denies its level",
			"policy", problem.Policy.ID, "level", problem.Policy.Level, "err", problem.Err)
	}

	authenticate, err := dashboardAuthenticator(ctx, opts, tasks, log)
	if err != nil {
		return err
	}
	login, err := dashboardLoginFlow(ctx, opts)
	if err != nil {
		return err
	}

	srv := compose(deps{
		Options:      opts,
		Log:          log,
		Store:        tasks,
		Chat:         chat,
		Health:       newReadinessRegistry(opts, tasks, chat, problems),
		ControlPlane: tasks.ControlPlane(),
		Identities:   tasks,
		Authenticate: authenticate,
		Login:        login,
		Access:       chain,
		Principals:   tasks,
		Denials:      tasks,
	})

	// Live activity has no in-process bus in this process: the pump reads
	// events back out of the State Store over the same cursor SSE catch-up
	// uses. Backgrounded so priming past a large events table cannot delay
	// the listener (docs/architecture/migration-decisions.md, "Dashboard
	// live event delivery").
	go func() {
		if err := srv.PumpEvents(ctx, opts.EventPollInterval); err != nil {
			log.Error("event pump stopped; the activity feed will only show history", "err", err)
		}
	}()
	liveCtx, stopLive := context.WithCancel(ctx)
	defer stopLive()
	go srv.RunLive(liveCtx)

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return fmt.Errorf("listen for ui: %w", err)
	}
	cleanups = append(cleanups, func() { _ = listener.Close() })
	log.Info(
		"archie-ui running",
		"addr", listener.Addr().String(),
		"open", webui.DashboardURL(listener.Addr().String(), opts.Token),
		"gateway", opts.Gateway.Target,
		"state", opts.State.Target,
	)

	return serve(ctx, listener, srv.Handler(), opts)
}

// serve runs handler on listener until ctx ends, then shuts the HTTP server
// down within ShutdownTimeout so in-flight dashboard requests finish before
// the contract clients close. A graceful shutdown that overruns its deadline
// is a normal shutdown under load, not a process failure: the remaining
// connections are force-closed and serve reports clean.
func serve(ctx context.Context, listener net.Listener, handler http.Handler, opts Options) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: opts.ReadHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// Shutdown returned once the drain deadline elapsed with a request
			// still in flight. Force-close what is left so shutdown finishes
			// and the process exits cleanly rather than carrying a deadline
			// error to the operator as a fatal exit (archie-core-u4xu).
			_ = server.Close()
		}
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
