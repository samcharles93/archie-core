// Command archie-gateway hosts the Gateway Service's gRPC contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/archied"
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func main() { os.Exit(run()) }

func run() int {
	var options archied.GatewayOptions
	showVersion := buildinfo.RegisterVersionFlag("archie-gateway")
	flag.StringVar(&options.Config, "config", configuration.DefaultConfigPath(), "configuration file or directory")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Listen, "listen", "", "gateway gRPC listen address (defaults to [services.gateway].listen, else 127.0.0.1:8585)")
	flag.StringVar(&options.Token, "token", "", "Bearer [REDACTED] required for a non-loopback listener (defaults to [services.gateway].target_token)")
	flag.Parse()
	showVersion()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archied.RunGateway(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
