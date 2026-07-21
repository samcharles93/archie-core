// Command archie-agent executes one unprivileged autonomous agent stage from
// a versioned JSON invocation on stdin and writes one response to stdout.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := agentexec.ServeOne(ctx, os.Stdin, os.Stdout, log); err != nil {
		log.Error("agent worker failed", "err", err)
		return 1
	}
	return 0
}
