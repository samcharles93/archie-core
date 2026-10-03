package archied

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/samcharles93/archie-core/internal/app/servicekit"

	natsio "github.com/nats-io/nats.go"
	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
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
	listen, err = servicekit.ResolveListen("gateway", options.Listen, b.cfg.Services.Get(config.ServiceNameGateway).Listen)
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
// *staterpc.Client via [services.state].target). The Gateway serves the
// conversation store from database_url and holds the Gateway serve claim for
// its whole life.
func RunGateway(ctx context.Context, options GatewayOptions) error {
	b := newBootstrap()
	b.processName = applystatus.Gateway
	defer b.cleanup()
	if err := b.loadConfig(ctx, options.Config, options.Overlay); err != nil {
		return err
	}
	b.log = b.log.With("component", "gateway")
	if err := b.openStores(ctx); err != nil {
		return err
	}
	// The Gateway also no longer opens archie.db directly: it consumes the
	// same remote State Store contract the daemon does via
	// [services.state].target.
	if err := b.openGatewayState(ctx); err != nil {
		return err
	}
	b.loadCatalog(ctx, options.Config)
	b.seedSoul(options.Config)
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
		endpoint, err = readEmbeddedNATSEndpoint(b.cfg.StateDir)
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
	// Held so /status can report this process's own broker connection: the
	// health source reads it through boot rather than dialling.
	b.taskActionsConn = nc
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
	return b.serveGatewayListener(ctx, listener, loopback, contract, opts)
}

// serveGatewayListener is the serving tail of RunGateway: the listener is
// bound, the process declares itself serving, and the chat contract answers
// until ctx ends.
func (b *boot) serveGatewayListener(ctx context.Context, listener net.Listener, loopback bool, contract gateway.ChatContract, opts []grpc.ServerOption) error {
	defer listener.Close()
	b.log.Info("archie-gateway running", "addr", listener.Addr().String(), "token_required", !loopback)
	// Boot is over and this listener is about to accept: the same fact
	// systemd's READY=1 asserts, so the announcement goes out here rather than
	// from a second notion of "started". Not later: Serve
	// begins accepting the moment it is called, and a unit whose
	// TimeoutStartSec expires first would restart a healthy process.
	b.announceReady()
	return serveGateway(ctx, listener, contract, b.chatSessionStore, opts)
}

func (b *boot) openGatewayState(ctx context.Context) error {
	if err := b.claimGatewayOwnership(ctx); err != nil {
		return err
	}
	if err := b.openStateStoreAdapter(ctx); err != nil {
		return err
	}
	if err := b.loadRuntimeConfig(ctx); err != nil {
		return fmt.Errorf("load runtime settings: %w", err)
	}
	go b.applyStatus.Run(ctx)
	return nil
}

func (b *boot) startGatewayRuntime(ctx context.Context, actor gateway.ChatTaskActor) (gateway.ChatContract, error) {
	b.bus = events.NewBus()
	b.addCleanup(b.bus.Close)
	b.capabilityHost = plugin.NewHost()
	b.startRateLimiter(ctx, b.cfg.Chat.RateLimit)
	// The Gateway composes the process-wide worktree manager before
	// setupGatewayChat; nothing in this process reads it today. Kept for the
	// manager's other consumers; it is the only part of the daemon's tree
	// composition the Gateway takes -- it must not own identity runners, so it
	// does not call buildTreesAndIdentities.
	b.buildWorktreeManager()
	// setupMemoryEngine must run before setupGatewayChat: setupGatewayChat
	// constructs the turn runner, which captures b.memEngines at
	// construction time (see the identical ordering note in main.go's Run).
	if err := b.setupMemoryEngine(); err != nil { //nolint:contextcheck // setupMemoryEngine owns its own lifecycle context, matching the daemon's setupMemoryEngine
		return nil, err
	}
	contract, err := b.setupGatewayChat(ctx, actor)
	if err != nil {
		return nil, err
	}
	if err := b.registerTools(); err != nil {
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

// inboundMessageHeadroomBytes leaves room for the protobuf framing and the
// rest of the request around an attachment at the messaging contract's
// ceiling, so the largest attachment a channel frontend may send is accepted
// rather than rejected as an oversized gRPC message.
const inboundMessageHeadroomBytes = 1 << 20

// gatewayServerOpts applies the transport security boundary: a loopback
// listener is served insecure with no token; any non-loopback address
// requires a Bearer [REDACTED], else the process fails closed. It mirrors
// stateStoreServerOpts, minus the State Store's task-scoped grants: the
// Gateway has one administrative token for all callers.
//
// The receive limit is raised above gRPC's 4 MB default because an inbound
// request carries a channel attachment's bytes: the channel frontend
// downloads the file, and this process -- which runs the turn -- holds no
// platform credential to fetch it again.
func gatewayServerOpts(listen, token string) (opts []grpc.ServerOption, loopback bool, err error) {
	recv := grpc.MaxRecvMsgSize(messaging.MaxInboundAttachmentBytes + inboundMessageHeadroomBytes)
	loopback, err = gatewayrpc.TargetIsLoopback(listen)
	if err != nil {
		return nil, false, err
	}
	if loopback {
		return []grpc.ServerOption{recv}, true, nil
	}
	if token == "" {
		return nil, false, fmt.Errorf(
			"gateway listen address %q is non-loopback; non-loopback exposure requires a Bearer [REDACTED] (--token / [services.gateway].target_token) or TLS",
			listen,
		)
	}
	return []grpc.ServerOption{
		recv,
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
