// Command archie-agent runs Archie's NATS-connected agent worker.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/agentworker"
)

type workerRunner func(context.Context, agentworker.Settings, *slog.Logger) error

func main() {
	os.Exit(run())
}

func run() int {
	return runCommand(os.Args[1:], os.Getenv, os.Stderr, agentworker.Run)
}

func runCommand(args []string, getenv func(string) string, stderr io.Writer, runWorker workerRunner) int {
	if len(args) > 0 && args[0] == "relay" {
		return runRelay(args[1:], stderr, agentworker.RunRelay)
	}
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(args[1:], stderr, agentworker.RunCaptureMCP)
	}
	flags := flag.NewFlagSet("archie-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	natsURLFlag := flags.String("nats-url", "", "NATS server URL (defaults to NATS_URL)")
	stateStoreURLFlag := flags.String("state-store-url", "", "State Store gRPC target (defaults to STATE_STORE_URL; required -- the legacy NATS storerpc path is deleted)")
	stateStoreTokenFlag := flags.String("state-store-token", "", "State Store bearer token (defaults to STATE_STORE_TOKEN)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	natsURL, natsToken := natsConnectionSettings(*natsURLFlag, getenv)
	if natsURL == "" {
		fmt.Fprintln(stderr, "error: -nats-url or NATS_URL is required")
		flags.Usage()
		return 1
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stateStoreURL := *stateStoreURLFlag
	if stateStoreURL == "" {
		stateStoreURL = getenv("STATE_STORE_URL")
	}
	if stateStoreURL == "" {
		fmt.Fprintln(stderr, "error: -state-store-url or STATE_STORE_URL is required (the legacy NATS storerpc path is deleted)")
		flags.Usage()
		return 1
	}
	stateStoreToken := *stateStoreTokenFlag
	if stateStoreToken == "" {
		stateStoreToken = getenv("STATE_STORE_TOKEN")
	}

	if err := runWorker(ctx, agentworker.Settings{
		NATSURL:          natsURL,
		NATSToken:        natsToken,
		StateStoreTarget: stateStoreURL,
		StateStoreToken:  stateStoreToken,
	}, log); err != nil {
		return 1
	}
	return 0
}

func natsConnectionSettings(flagURL string, getenv func(string) string) (url, token string) {
	url = flagURL
	if url == "" {
		url = getenv("NATS_URL")
	}
	return url, getenv("NATS_TOKEN")
}

type relayServer func(ctx context.Context, forwards map[string]string) error

// runRelay is the sandbox egress relay: the container on both a sandbox's
// isolated network and the host-reachable one, forwarding a fixed set of
// ports to the proxy, NATS and the State Store.
func runRelay(args []string, stderr io.Writer, serve relayServer) int {
	flags := flag.NewFlagSet("archie-agent relay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	forwards := map[string]string{}
	flags.Func("forward", "listen=target: forward connections on listen to target (repeatable)", func(v string) error {
		listen, target, ok := strings.Cut(v, "=")
		if !ok || listen == "" || target == "" {
			return fmt.Errorf("%q is not listen=target", v)
		}
		if _, _, err := net.SplitHostPort(listen); err != nil {
			return fmt.Errorf("listen address %q: %w", listen, err)
		}
		if _, _, err := net.SplitHostPort(target); err != nil {
			return fmt.Errorf("target %q: %w", target, err)
		}
		if _, dup := forwards[listen]; dup {
			return fmt.Errorf("listen address %q is forwarded twice", listen)
		}
		forwards[listen] = target
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(forwards) == 0 {
		fmt.Fprintln(stderr, "error: at least one -forward is required")
		flags.Usage()
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, forwards); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

type mcpServer func(ctx context.Context, specPath, capturesPath string) error

// runMCP serves a stage's capture tools to a harness over stdio. The
// harness starts it, so it runs as the harness user and holds no secrets.
func runMCP(args []string, stderr io.Writer, serve mcpServer) int {
	flags := flag.NewFlagSet("archie-agent mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	spec := flags.String("spec", "", "JSON file listing the stage's capture tools")
	captures := flags.String("captures", "", "file accepted capture calls are appended to")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *spec == "" || *captures == "" {
		fmt.Fprintln(stderr, "error: -spec and -captures are required")
		flags.Usage()
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, *spec, *captures); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
