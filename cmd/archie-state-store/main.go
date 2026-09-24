// Command archie-state-store hosts the State Store's gRPC contract and is the
// only process that owns the PostgreSQL stores behind it. See
// docs/prds/state-store-contract.md §5, §9 and §11.
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
	"github.com/samcharles93/archie-core/internal/buildinfo"
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
	showVersion := buildinfo.RegisterVersionFlag("archie-state-store")
	flag.StringVar(&options.Config, "config", configuration.DefaultConfigPath(), "configuration file or directory")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Listen, "listen", "", "state store gRPC listen address (defaults to [services.state].listen, else 127.0.0.1:9090)")
	flag.StringVar(&options.Token, "token", "", "bearer token required for a non-loopback listener (defaults to [services.state].target_token)")
	flag.StringVar(&options.ReadyAddr, "ready-addr", "", "optional readiness HTTP listen address (e.g. 127.0.0.1:9091)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: archie-state-store [flags]   # serve the State Store gRPC contract\n\noffline recovery: archie-state-store <backup|restore|validate|rollback|import> -h\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	showVersion()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archied.RunStateStore(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
