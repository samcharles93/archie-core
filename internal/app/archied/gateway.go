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
	host, _, err := net.SplitHostPort(options.Listen)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("gateway listen address must use a loopback IP; use a secured tunnel for remote access")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Listen)
	if err != nil {
		return fmt.Errorf("listen for gateway: %w", err)
	}
	defer listener.Close()
	b.log.Info("archie-gateway running", "addr", listener.Addr().String())
	return serveGateway(ctx, listener, contract, b.chatSessionStore)
}

func (b *boot) startGatewayRuntime(ctx context.Context, actor gateway.ChatTaskActor) (gateway.ChatContract, error) {
	b.bus = events.NewBus()
	b.addCleanup(b.bus.Close)
	b.capabilityHost = plugin.NewHost()
	contract := b.setupGatewayChat(ctx, actor)
	// Memory is opened with its own long-lived context inside (matching the
	// daemon path via setupMemoryAll): shutdown of the file-backed provider
	// outlives the boot context by design, so the legacy manager wires itself
	// with a background context rather than the gateway's boot ctx.
	if err := b.setupMemory(); err != nil { //nolint:contextcheck // setupMemory owns its lifecycle contexts, matching the daemon's setupMemoryAll
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

func serveGateway(ctx context.Context, listener net.Listener, contract gateway.ChatContract, sessions gateway.SessionStore) error {
	server := grpc.NewServer()
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
