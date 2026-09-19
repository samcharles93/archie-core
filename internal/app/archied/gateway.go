package archied

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	natsio "github.com/nats-io/nats.go"
	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
	"github.com/samcharles93/archie-core/internal/plugin"
)

// GatewayOptions contains process inputs for the standalone Gateway.
type GatewayOptions struct {
	Config  string
	Overlay string
	Listen  string
	// Token is the Bearer [REDACTED] the Gateway server validates for a
	// non-loopback listener. Empty falls back to
	// [services.gateway].target_token.
	Token string
}

// gatewayListenAndToken resolves the two per-listener inputs RunGateway needs:
// the address it binds and the bearer token a non-loopback bind validates.
//
// Both are flag-first and config-second, and both fail closed rather than
// inventing a value -- resolveServiceListen explains why an empty address is an
// error rather than a default.
func gatewayListenAndToken(b *boot, options GatewayOptions) (listen, token string, err error) {
	listen, err = resolveServiceListen("gateway", options.Listen, b.cfg.Services.Get(config.ServiceNameGateway).Listen)
	if err != nil {
		return "", "", err
	}
	token = options.Token
	if token == "" {
		token = b.cfg.Services.ResolvedToken(config.ServiceNameGateway, b.secrets.Getenv)
	}
	return listen, token, nil
}

// RunGateway owns conversation persistence, model runtime and tool-provider
// lifecycles. Task-store adapters access the State Store contract (remote
// *staterpc.Client via [services.state].target), not archie.db directly: the
// single SQLite file is owned by archie-state-store, and the Gateway keeps its
// own already-separate session SQLite (docs/prds/state-store-contract.md §12
// step 8), which is out of state-store scope.
func RunGateway(ctx context.Context, options GatewayOptions) error {
	b := newBootstrap()
	defer b.cleanup()
	if err := b.loadConfig(ctx, options.Config, options.Overlay, false); err != nil {
		return err
	}
	b.log = b.log.With("component", "gateway")
	if err := b.openStores(ctx); err != nil {
		return err
	}
	// The Gateway also no longer opens archie.db directly: it consumes the
	// same remote State Store contract the daemon does via
	// [services.state].target (docs/prds/state-store-contract.md §12 step 7).
	// Its own session SQLite is untouched and stays Gateway-owned.
	if err := b.openStateStoreAdapter(); err != nil {
		return err
	}
	b.loadCatalog(ctx, options.Config)
	url, token := b.cfg.NATS.URL, ""
	var nc *natsio.Conn
	var err error
	if b.cfg.NATS.Mode == config.NATSModeExternal { //nolint:nestif // external and embedded transports have distinct credential and discovery flows
		token, err = configuredNATSToken(b.cfg.NATS, os.Getenv)
		if err != nil {
			return err
		}
		nc, err = natsio.Connect(url, natsio.Token(token))
	} else {
		var endpoint embeddedNATSEndpoint
		endpoint, err = readEmbeddedNATSEndpoint(b.cfg.DBPath)
		if err == nil {
			url, token = endpoint.URL, endpoint.Token
			nc, err = natsio.Connect(url, natsio.Token(token))
		}
		if nc == nil || err != nil {
			url, token, err = b.startEmbeddedNATS(ctx)
			if err != nil {
				return err
			}
			nc, err = natsio.Connect(url, natsio.Token(token))
		}
	}
	if err != nil {
		return fmt.Errorf("connect gateway task actions: %w", err)
	}
	b.addCleanup(nc.Close)
	contract, err := b.startGatewayRuntime(ctx, taskactions.Client{Conn: nc, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	listen, gatewayToken, err := gatewayListenAndToken(b, options)
	if err != nil {
		return err
	}
	//nolint:contextcheck // grpc.StreamServerInterceptor has no context.Context parameter; gatewayrpc.StreamServerInterceptor derives its context from stream.Context() instead
	opts, loopback, err := gatewayServerOpts(listen, gatewayToken)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("listen for gateway: %w", err)
	}
	defer listener.Close()
	b.log.Info("archie-gateway running", "addr", listener.Addr().String(), "token_required", !loopback)
	return serveGateway(ctx, listener, contract, b.chatSessionStore, opts)
}

func (b *boot) startGatewayRuntime(ctx context.Context, actor gateway.ChatTaskActor) (gateway.ChatContract, error) {
	b.bus = events.NewBus()
	b.addCleanup(b.bus.Close)
	b.capabilityHost = plugin.NewHost()
	b.startRateLimiter(ctx, b.cfg.Chat.RateLimit)
	// setupMemoryEngine must run before setupGatewayChat: setupGatewayChat
	// constructs the turn runner, which captures b.memEngines at
	// construction time (see the identical ordering note in main.go's Run).
	if err := b.setupMemoryEngine(); err != nil { //nolint:contextcheck // setupMemoryEngine owns its own lifecycle context, matching the daemon's setupMemoryEngine
		return nil, err
	}
	contract := b.setupGatewayChat(ctx, actor)
	if err := b.registerTools(ctx); err != nil {
		return nil, err
	}
	b.registerStandaloneTools()
	b.addCleanup(shutdownCapabilityHost(b.capabilityHost, b.log))
	if err := b.capabilityHost.Start(ctx); err != nil {
		return nil, fmt.Errorf("start gateway tool providers: %w", err)
	}
	for _, skipped := range b.providerRegistry.Skipped() {
		b.log.Warn("gateway tool provider unavailable", "provider", skipped.ID, "err", skipped.Err)
	}
	return contract, nil
}

// gatewayServerOpts applies the transport security boundary: a loopback
// listener is served insecure with no token; any non-loopback address
// requires a Bearer [REDACTED], else the process fails closed. It mirrors
// stateStoreServerOpts, minus the State Store's task-scoped grants: the
// Gateway has one administrative token for all callers.
func gatewayServerOpts(listen, token string) (opts []grpc.ServerOption, loopback bool, err error) {
	loopback, err = gatewayrpc.TargetIsLoopback(listen)
	if err != nil {
		return nil, false, err
	}
	if loopback {
		return nil, true, nil
	}
	if token == "" {
		return nil, false, fmt.Errorf(
			"gateway listen address %q is non-loopback; non-loopback exposure requires a Bearer [REDACTED] (--token / [services.gateway].target_token) or TLS",
			listen,
		)
	}
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(gatewayrpc.UnaryServerInterceptor(token)),
		grpc.ChainStreamInterceptor(gatewayrpc.StreamServerInterceptor(token)),
	}, false, nil
}

func serveGateway(ctx context.Context, listener net.Listener, contract gateway.ChatContract, sessions gateway.SessionStore, opts []grpc.ServerOption) error {
	server := grpc.NewServer(opts...)
	gatewayrpc.RegisterServer(server, contract, sessions)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		// A client can keep a streaming RPC open indefinitely. Allow draining,
		// then force cancellation before closing the session store and tools.
		timer := time.AfterFunc(10*time.Second, server.Stop)
		server.GracefulStop()
		timer.Stop()
		return nil
	case err := <-serveErr:
		return err
	}
}
