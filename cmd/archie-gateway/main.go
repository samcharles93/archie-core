// Command archie-gateway hosts the Gateway Service's gRPC contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/archied"
)

func main() { os.Exit(run()) }

func run() int {
	base, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var options archied.GatewayOptions
	flag.StringVar(&options.Config, "config", filepath.Join(base, "archie", "config.toml"), "configuration file or directory")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Listen, "listen", "127.0.0.1:8585", "gateway gRPC listen address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archied.RunGateway(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
