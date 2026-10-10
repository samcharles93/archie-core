package archieui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/harnessrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/webui"
)

// buildAccessChain builds the live access engine over the stored policies. A
// State Store that is not up yet yields a chain with no engine: it refuses
// every request until the first load succeeds and keeps retrying, so a slow
// database or a bad start order closes the dashboard rather than opening it.
func buildAccessChain(ctx context.Context, store *staterpc.Client, log *slog.Logger) (*infraaccess.Live, error) {
	live := infraaccess.NewPending(store.ListPolicies, log)
	if err := live.Reload(ctx); err != nil {
		if status.Code(err) != codes.Unavailable {
			return nil, err
		}
		log.Warn("access policies unavailable; the dashboard refuses API requests until they load", "err", err)
	}
	go live.Run(ctx, applystatus.RestampInterval)
	return live, nil
}

// Run serves the dashboard until ctx is cancelled. It resolves its own
// options, dials the Gateway and State Store contracts itself, composes the
// HTTP surface over them, and unwinds listener then clients in reverse order
// on shutdown.
func Run(ctx context.Context, options Options) error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("component", "ui")
	log, feed := servicekit.Diagnostics(log, presence.UI)
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
	tasks, closeState, err := staterpc.Dial(opts.State.Target, presence.UI, opts.State.Token)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, closeState)
	chat, closeGateway, err := gatewayrpc.Dial(opts.Gateway.Target, opts.Gateway.Token)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, closeGateway)
	chain, err := buildAccessChain(ctx, tasks, log)
	if err != nil {
		return err
	}
	provider, err := signInProvider(opts)
	if err != nil {
		return err
	}
	authenticate := dashboardAuthenticator(provider, opts, tasks, log)
	login, err := dashboardLoginFlow(provider, opts)
	if err != nil {
		return err
	}
	srv := compose(ctx, deps{
		Options:      opts,
		Log:          log,
		Store:        tasks,
		Chat:         chat,
		Updates:      controlplanerpc.UpdateService(controlplanerpc.NewRPCClient(tasks.ControlPlane()), filepath.Join(configuration.DefaultWorkDir(), "ui-update-deferrals.json"), ""),
		Health:       newReadinessRegistry(opts, tasks, chat, chain, provider),
		ControlPlane: tasks.ControlPlane(),
		Identities:   tasks,
		Authenticate: authenticate,
		Login:        login,
		Access:       chain,
		Principals:   tasks,
		Denials:      tasks,
	})
	stopLogs, err := wireServiceLogs(opts, srv, tasks, chat, feed)
	if err != nil {
		return err
	}
	cleanups = append(cleanups, stopLogs)
	if err := wireHarness(opts, srv, &cleanups); err != nil {
		return err
	}
	// Priming event history must not delay the listener.
	go pumpEvents(ctx, srv, opts.EventPollInterval, log)
	go presence.Run(ctx, presence.UI, servicekit.Build(), tasks, srv.Health, log)
	liveCtx, stopLive := context.WithCancel(ctx)
	defer stopLive()
	go srv.RunLive(liveCtx)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return fmt.Errorf("listen for ui: %w", err)
	}
	cleanups = append(cleanups, func() { _ = listener.Close() })
	attrs := []any{
		"addr", listener.Addr().String(),
		"open", webui.DashboardURL(listener.Addr().String()),
		"gateway", opts.Gateway.Target,
		"state", opts.State.Target,
	}
	// Name the file holding the token, never the token itself: this line
	// reaches the journal and any log an operator pastes.
	if opts.TokenFile != "" {
		attrs = append(attrs, "token_file", opts.TokenFile)
	}
	log.Info("archie-ui running", attrs...)
	return serve(ctx, listener, srv.Handler(), opts)
}

// wireHarness attaches the setup terminal to the daemon's session contract
// when the operator configured one, and registers its cleanup.
func wireHarness(opts Options, srv *webui.Server, cleanups *[]func()) error {
	if opts.Harness.Target == "" {
		return nil
	}
	harness, closeHarness, err := harnessrpc.Dial(opts.Harness.Target, opts.Harness.Token)
	if err != nil {
		return err
	}
	srv.HarnessTerminal = harness
	*cleanups = append(*cleanups, closeHarness)
	return nil
}

// serve runs handler until ctx ends, then shuts down within
// ShutdownTimeout.
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
			// error to the operator as a fatal exit.
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

func pumpEvents(ctx context.Context, srv *webui.Server, interval time.Duration, log *slog.Logger) {
	if err := srv.PumpEvents(ctx, interval); err != nil {
		log.Error("event pump stopped; the activity feed will only show history", "err", err)
	}
}
