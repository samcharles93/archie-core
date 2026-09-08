// Command archie-ui serves the operator dashboard and the compiled SPA as its
// own process, against a Gateway and a State Store reached over their gRPC
// contracts. It is the .5.2 companion to archie-gateway and
// archie-state-store: a thin main that parses process inputs and hands them
// to archieui.Run.
//
// See docs/prds/ui-service-boundary.md for the ratified boundary.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/archieui"
)

func main() { os.Exit(run()) }

func run() int {
	base, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var options archieui.Options
	flag.StringVar(&options.Config, "config", filepath.Join(base, "archie", "config.toml"), "configuration file or directory (only [services.*] targets and [web] are read)")
	flag.StringVar(&options.Overlay, "config-overlay", "", "configuration overlay file or directory")
	flag.StringVar(&options.Listen, "listen", "", "dashboard HTTP listen address (defaults to [web].listen, else 127.0.0.1:8484)")
	flag.StringVar(&options.Token, "token", "", "dashboard token; required for a non-loopback listener")
	flag.StringVar(&options.TokenFile, "token-file", "", "file holding the dashboard token, minted on first use")
	flag.StringVar(&options.Gateway.Target, "gateway-target", "", "archie-gateway gRPC address (defaults to [services.gateway].target)")
	flag.StringVar(&options.Gateway.Token, "gateway-token", "", "bearer token presented to archie-gateway")
	flag.StringVar(&options.State.Target, "state-target", "", "archie-state-store gRPC address (defaults to [services.state].target)")
	flag.StringVar(&options.State.Token, "state-token", "", "bearer token presented to archie-state-store")
	flag.DurationVar(&options.DependencyTimeout, "dependency-timeout", 0, "per-dependency readiness probe timeout (default 5s)")
	flag.DurationVar(&options.EventPollInterval, "event-poll-interval", 0, "how often the activity feed polls the State Store for new events (default 1s)")
	// BoolFunc rather than BoolVar so an unset flag stays distinguishable
	// from an explicit -trust-forwarded-headers=false, which must be able to
	// override a configuration file that enables it.
	flag.BoolFunc("trust-forwarded-headers", "trust X-Forwarded-Proto/Host when validating Origin (only behind a trusted proxy)", func(v string) error {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return err
		}
		options.TrustForwardedHeaders = &parsed
		return nil
	})
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archieui.Run(ctx, options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
