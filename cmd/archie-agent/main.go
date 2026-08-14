// Command archie-agent runs Archie's NATS-connected agent worker.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/agentworker"
)

const consumerName = "archie-agent"

type workerRunner func(context.Context, agentworker.Settings, *slog.Logger) error

func main() {
	os.Exit(run())
}

func run() int {
	return runCommand(os.Args[1:], os.Getenv, os.Stderr, agentworker.Run)
}

func runCommand(args []string, getenv func(string) string, stderr io.Writer, runWorker workerRunner) int {
	flags := flag.NewFlagSet("archie-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	natsURLFlag := flags.String("nats-url", "", "NATS server URL (defaults to NATS_URL)")
	consumer := flags.String("consumer", consumerName, "JetStream consumer name")
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

	if err := runWorker(ctx, agentworker.Settings{
		NATSURL:   natsURL,
		NATSToken: natsToken,
		Consumer:  *consumer,
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
