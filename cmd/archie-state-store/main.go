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
	"strings"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/archied"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func main() { os.Exit(run()) }

func run() int {
	// A leading non-flag argument selects a recovery subcommand; anything else,
	// including no arguments at all, serves the State Store contract. An
	// unrecognised word is refused rather than ignored, so a mistyped recovery
	// command never leaves an operator watching a server start instead.
	if args := os.Args[1:]; len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return runRecovery(args, os.Stdout, os.Stderr)
	}
	var options archied.StateStoreOptions
	flag.StringVar(&options.Config, "config", configuration.DefaultConfigPath(), "configuration file or directory")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Listen, "listen", "", "state store gRPC listen address (defaults to [services.state].listen, else 127.0.0.1:9090)")
	flag.StringVar(&options.Token, "token", "", "bearer token required for a non-loopback listener (defaults to [services.state].target_token)")
	flag.StringVar(&options.ReadyAddr, "ready-addr", "", "optional readiness HTTP listen address (e.g. 127.0.0.1:9091)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: archie-state-store [flags]   # serve the State Store gRPC contract\n\noffline recovery: archie-state-store <backup|restore|validate|rollback> -h\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archied.RunStateStore(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
