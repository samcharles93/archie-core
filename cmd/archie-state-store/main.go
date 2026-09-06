// Command archie-state-store hosts the State Store's gRPC contract and owns
// the single archie.db SQLite file. It is the .4.3 companion to the daemon's
// in-process State Store server: the same StateStoreService .4.2 serves, but
// extracted into its own process with its own store lifecycle.
//
// The gateway's session SQLite is owned by the separate archie-gateway process
// and is untouched here. See docs/prds/state-store-contract.md (rev. 2c) §5,
// §9 and §11.
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
	var options archied.StateStoreOptions
	flag.StringVar(&options.Config, "config", filepath.Join(base, "archie", "config.toml"), "configuration file or directory")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Listen, "listen", "127.0.0.1:9090", "state store gRPC listen address")
	flag.StringVar(&options.Token, "token", "", "bearer token required for a non-loopback listener (defaults to [services.state].target_token)")
	flag.StringVar(&options.ReadyAddr, "ready-addr", "", "optional readiness HTTP listen address (e.g. 127.0.0.1:9091)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archied.RunStateStore(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
