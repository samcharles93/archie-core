package archiegateway

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/plugin"
)

// serveGatewayListener is the serving tail of Run: the listener is
// bound, the process declares itself serving, and the chat contract answers
// until ctx ends.
func (b *server) serveGatewayListener(ctx context.Context, listener net.Listener, loopback bool, contract gateway.ChatContract, opts []grpc.ServerOption) error {
	defer listener.Close()
	b.log.Info("archie-gateway running", "addr", listener.Addr().String(), "token_required", !loopback)
	// Boot is over and this listener is about to accept: the same fact
	// systemd's READY=1 asserts, so the announcement goes out here rather than
	// from a second notion of "started". Not later: Serve
	// begins accepting the moment it is called, and a unit whose
	// TimeoutStartSec expires first would restart a healthy process.
	b.announceReady()
	return serveGateway(ctx, listener, contract, b.chatSessionStore, b.chatCatalog(), opts)
}

func (b *server) startGatewayRuntime(ctx context.Context, actor gateway.ChatTaskActor) (gateway.ChatContract, error) {
	b.bus = events.NewBus()
	b.addCleanup(b.bus.Close)
	b.capabilityHost = plugin.NewHost()
	b.startRateLimiter(ctx, b.cfg.Chat.RateLimit)
	// setupMemoryEngine must run before setupGatewayChat: setupGatewayChat
	// constructs the turn runner, which captures b.memEngines at
	// construction time.
	if err := b.setupMemoryEngine(); err != nil { //nolint:contextcheck // setupMemoryEngine owns its own lifecycle context
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
	b.serveSkills(ctx)
	b.addCleanup(shutdownCapabilityHost(ctx, b.capabilityHost, b.log))
	if err := b.capabilityHost.Start(ctx); err != nil {
		return nil, fmt.Errorf("start gateway tool providers: %w", err)
	}
	for _, skipped := range b.providerRegistry.Skipped() {
		b.log.Warn("gateway tool provider unavailable", "provider", skipped.ID, "err", skipped.Err)
	}
	if err := b.startRuntimeWatches(ctx); err != nil {
		return nil, fmt.Errorf("runtime resource watches: %w", err)
	}
	b.startModelCatalogRefresh(ctx)
	// Curators wake on this process's turns and read its conversations.
	b.setupCurators(ctx)
	if err := b.curatorRuntime.Start(ctx); err != nil {
		b.log.Error("curator runtime startup", "err", err)
	}
	defs, version, err := b.controlPlane.Curators(ctx)
	if err == nil {
		err = b.applyCurators(ctx, defs)
	}
	b.applyStatus.Report(ctx, controlplane.CuratorsKind, version, err)
	if err != nil {
		b.log.Error("curator definitions apply failed", "err", err)
	}
	if err := b.watchCurators(ctx, version); err != nil {
		return nil, fmt.Errorf("watch curators: %w", err)
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

func serveGateway(ctx context.Context, listener net.Listener, contract gateway.ChatContract, sessions gateway.SessionStore, catalog gatewayrpc.Catalog, opts []grpc.ServerOption) error {
	server := grpc.NewServer(opts...)
	gatewayrpc.RegisterServer(server, contract, sessions, catalog)
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
