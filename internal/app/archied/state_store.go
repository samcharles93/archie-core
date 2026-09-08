// state_store.go composes the standalone archie-state-store process. It mirrors
// gateway.go / cmd/archie-gateway: the binary is a thin main that passes
// process inputs into RunStateStore, which owns the single archie.db SQLite
// file, registers every StateStore gRPC handler (the same service .4.2 serves
// in-process on the daemon), and shuts down cleanly on ctx cancellation.
//
// See docs/prds/state-store-contract.md (rev. 2c) -- the single authoritative
// design surface -- and in particular §5 (binary layout), §9 (transport
// security boundary) and §11 (the store service owns its own DB lifecycle).
package archied

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

// StateStoreOptions contains process inputs for the standalone State Store.
type StateStoreOptions struct {
	Config  string
	Overlay string
	// Listen is the gRPC listen address. Loopback binds (127.0.0.1, ::1,
	// localhost) serve insecure with no token; any non-loopback address
	// requires a non-empty token (or TLS), else the process fails closed
	// (state-store-contract.md §9).
	Listen string
	// Token is the bearer token the State Store server validates for a
	// non-loopback listener. Empty falls back to [services.state].target_token.
	Token string
	// ReadyAddr, when non-empty, starts an HTTP readiness surface on the
	// address: GET /healthz (liveness) and GET /health/detailed (the
	// state_db probe). Empty disables it.
	ReadyAddr string
}

// RunStateStore owns the single archie.db SQLite file, registers every
// StateStore gRPC handler, and serves until ctx is cancelled. It opens only
// the task store -- the gateway's session SQLite is owned by the separate
// archie-gateway process and is deliberately untouched (state-store-contract
// rev. 2c §12 step 8). The store service owns its own DB lifecycle, so
// b.cleanup() is the sole owner closing b.st here (in-process owner).
func RunStateStore(ctx context.Context, options StateStoreOptions) error {
	b := newBootstrap()
	defer b.cleanup()
	if err := b.loadConfig(ctx, options.Config, options.Overlay, false); err != nil {
		return err
	}
	b.log = b.log.With("component", "state-store")
	if err := b.openStateStore(ctx); err != nil {
		return err
	}

	token := options.Token
	if token == "" {
		token = stateStoreResolvedToken(b.cfg.Services.State, b.secrets)
	}
	grants := &staterpc.TaskGrants{}
	//nolint:contextcheck // grpc.StreamServerInterceptor has no context.Context parameter; TaskGrants.StreamInterceptor derives its context from stream.Context() instead
	opts, loopback, err := stateStoreServerOpts(options.Listen, token, grants)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Listen)
	if err != nil {
		return fmt.Errorf("listen for state store: %w", err)
	}
	defer listener.Close()
	b.log.Info("archie-state-store running", "addr", listener.Addr().String(), "token_required", !loopback)

	if options.ReadyAddr != "" {
		if err := b.startStateStoreReadiness(ctx, options.ReadyAddr); err != nil {
			return err
		}
	}

	return serveStateStore(ctx, listener, b.stateStoreDeps(grants), opts)
}

// openStateStore opens the single task-store SQLite file exactly once for
// service ownership. It resolves secrets (for the binding cipher) but does
// NOT open gateway chat sessions -- those live in the separate archie-gateway
// process on their own SQLite file and are out of state-store scope.
func (b *boot) openStateStore(ctx context.Context) error {
	cfg, log := b.cfg, b.log
	secrets, err := configuredSecretRegistry(&cfg, log)
	if err != nil {
		log.Error("configure secrets", "err", err)
		return err
	}
	b.secrets = secrets
	bindingCipher, err := bindingCipherFromConfig(cfg, secrets)
	if err != nil {
		log.Error("configure bindings cipher", "err", err)
		return err
	}
	st, err := openProductionTaskStore(ctx, taskDBPath(cfg.DBPath), store.WithBindingCipher(bindingCipher))
	if err != nil {
		log.Error("open state store", "err", err)
		return err
	}
	b.st = st
	b.addCleanup(func() {
		if err := st.Close(); err != nil {
			log.Error("close state store", "err", err)
		}
	})
	return nil
}

// stateStoreDeps assembles the store surfaces the StateStore service fronts.
// b.st is the narrow store.TaskStore; the wide *store.Store also implements
// the capture/mapping/binding surfaces, so each is asserted here (the same
// pattern the daemon's wireWebStoreSurfaces uses) and a store that lacks one
// degrades that group rather than aborting boot.
func (b *boot) stateStoreDeps(grants *staterpc.TaskGrants) staterpc.Deps {
	deps := staterpc.Deps{Tasks: b.st, Log: b.log, Grants: grants}
	if cs, ok := b.st.(store.CaptureStore); ok {
		deps.Captures = cs
	}
	if ms, ok := b.st.(store.MappingStore); ok {
		deps.Mappings = ms
	}
	if bs, ok := b.st.(store.BindingStore); ok {
		deps.Bindings = bs
	}
	if bd, ok := b.st.(store.BindingDispatcher); ok {
		deps.BindingDispatcher = bd
	}
	if btc, ok := b.st.(store.BindingTaskCreator); ok {
		deps.BindingTaskCreator = btc
	}
	return deps
}

// serveStateStore registers the StateStore gRPC service and serves it until
// ctx ends, draining in-flight calls before returning (mirroring serveGateway
// in gateway.go, minus the gateway's session-store argument).
func serveStateStore(ctx context.Context, listener net.Listener, deps staterpc.Deps, opts []grpc.ServerOption) error {
	server := grpc.NewServer(opts...)
	staterpc.RegisterServer(server, deps)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		timer := time.AfterFunc(10*time.Second, server.Stop)
		server.GracefulStop()
		timer.Stop()
		return nil
	case err := <-serveErr:
		return err
	}
}

// startStateStoreReadiness starts a minimal HTTP readiness surface on
// readyAddr: GET /healthz and /health answer 200 as a liveness probe (the
// process is up), while GET /health/detailed runs the state_db readiness probe
// and answers 503 when the store is degraded. It is the standalone process's
// counterpart to the daemon's /healthz + /health/detailed (internal/webui).
func (b *boot) startStateStoreReadiness(ctx context.Context, readyAddr string) error {
	registry := health.NewRegistry(readiness.NewStoreProbe(b.st))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /health/detailed", func(w http.ResponseWriter, r *http.Request) {
		report := registry.Run(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if report.Status != health.StatusOK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(report)
	})
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", readyAddr)
	if err != nil {
		return fmt.Errorf("listen for state store readiness: %w", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			b.log.Error("state store readiness server stopped", "err", err)
		}
	}()
	b.addCleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})
	b.log.Info("state store readiness listening", "addr", "http://"+listener.Addr().String())
	return nil
}

// stateStoreServerOpts applies the transport security boundary (§9): a
// loopback listener is served insecure with no token; any non-loopback
// address requires a bearer token, else the process fails closed. It cannot
// enforce TLS/mTLS (an operator decision), so it narrows the non-loopback
// path to token auth -- the same confinement philosophy as the gateway's
// loopback-only --listen rule.
func stateStoreServerOpts(listen, token string, grants *staterpc.TaskGrants) (opts []grpc.ServerOption, loopback bool, err error) {
	loopback, err = staterpc.TargetIsLoopback(listen)
	if err != nil {
		return nil, false, err
	}
	if loopback {
		return nil, true, nil
	}
	if token == "" {
		return nil, false, fmt.Errorf(
			"state store listen address %q is non-loopback; non-loopback exposure requires a bearer token (--token / [services.state].target_token) or TLS, mirroring the gateway's --listen confinement (docs/prds/state-store-contract.md §9)",
			listen,
		)
	}
	// grants.UnaryInterceptor/StreamInterceptor separate the daemon's own
	// administrative token (full access) from a container's task-scoped
	// grant (Update/Transition/InsertEvent on its own task ID only), rather
	// than the single all-or-nothing token check this replaced.
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(grants.UnaryInterceptor(token)),
		grpc.ChainStreamInterceptor(grants.StreamInterceptor(token)),
	}, false, nil
}

// constantTimeTokenValidator returns a staterpc.TokenValidator that accepts
// exactly the configured token, compared in constant time so a timing side
// channel cannot leak how many bytes matched (the standalone server's
// equivalent of the earlier in-process interceptor's token check).
func constantTimeTokenValidator(token string) staterpc.TokenValidator {
	return func(candidate string) bool {
		return subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1
	}
}
