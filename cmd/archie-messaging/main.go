// Command archie-messaging hosts the external channel frontends as their own
// process, against a Gateway reached over its gRPC contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/archiemessaging"
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func main() { os.Exit(run()) }

func run() int {
	var options archiemessaging.Options
	showVersion := buildinfo.RegisterVersionFlag("archie-messaging")
	flag.StringVar(&options.Config, "config", configuration.DefaultConfigPath(), "configuration file or directory")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Gateway.Target, "gateway-target", "", "archie-gateway gRPC address (defaults to [services.gateway].target, else 127.0.0.1:8585)")
	flag.StringVar(&options.Gateway.Token, "gateway-token", "", "bearer token presented to archie-gateway")
	flag.StringVar(&options.StateStore.Target, "state-store-target", "", "archie-state-store gRPC address (defaults to [services.state].target)")
	flag.StringVar(&options.StateStore.Token, "state-store-token", "", "bearer token presented to archie-state-store")
	flag.DurationVar(&options.DependencyTimeout, "dependency-timeout", 0, "readiness probe timeout (default 5s)")
	flag.DurationVar(&options.ShutdownTimeout, "shutdown-timeout", 0, "shutdown timeout (default 5s)")
	flag.Parse()
	showVersion()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := archiemessaging.Run(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
