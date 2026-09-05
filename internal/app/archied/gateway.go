package archied

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
)

// RunGateway starts the standalone Gateway Service. The service owns the
// conversation store and the Gateway composition; the daemon reaches it only
// through the generated ChatContract RPC.
func RunGateway() int {
	args := gatewayArgs()
	if args.version {
		fmt.Printf("archie-gateway %s\n", gatewayVersion)
		return 0
	}

	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, stop := signal.NotifyContext(root, os.Interrupt, syscall.SIGTERM)
	defer stop()

	b := newBootstrap()
	if err := b.loadConfig(ctx, args.config, args.overlay, false); err != nil {
		return 1
	}
	defer b.cleanup()
	if err := b.openStores(ctx); err != nil {
		return 1
	}
	b.loadCatalog(ctx, args.config)
	// setupLLMAndChat also wires the task adapters used by Gateway tools. It
	// normally publishes a dashboard contract, but the standalone service
	// publishes that same local adapter over gRPC below.
	b.setupObservability()
	b.cfg.Services.Gateway.Mode = "inproc"
	b.cfg.Services.Gateway.Target = ""
	b.setupLLMAndChat(ctx)
	if b.web == nil || b.web.Chat == nil || b.web.Chat.Contract == nil {
		b.log.Error("gateway contract was not composed")
		return 1
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", args.listen)
	if err != nil {
		b.log.Error("listen for gateway", "addr", args.listen, "err", err)
		return 1
	}
	defer listener.Close()
	server := grpc.NewServer()
	gatewayrpc.RegisterServer(server, b.web.Chat.Contract)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	b.log.Info("archie-gateway running", "addr", listener.Addr().String())

	select {
	case <-ctx.Done():
		server.GracefulStop()
		return 0
	case err := <-serveErr:
		if err != nil {
			b.log.Error("gateway server stopped", "err", err)
			return 1
		}
		return 0
	}
}

type gatewayRunArgs struct {
	config  string
	overlay string
	listen  string
	version bool
}

func gatewayArgs() gatewayRunArgs {
	args := gatewayRunArgs{config: filepathConfigHome()}
	flag.StringVar(&args.config, "config", args.config, "path to a TOML/YAML config file or configuration directory")
	flag.StringVar(&args.overlay, "config-overlay", "", "path to a TOML/YAML overlay file or configuration directory")
	flag.StringVar(&args.listen, "listen", "127.0.0.1:8585", "gateway gRPC listen address")
	flag.BoolVar(&args.version, "version", false, "print the Gateway Service version and exit")
	flag.Parse()
	return args
}

func filepathConfigHome() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = home + "/.config"
		}
	}
	return base + "/archie/config.toml"
}
