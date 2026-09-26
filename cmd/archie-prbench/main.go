// Command archie-prbench scores the PR reviewer on the Martian
// Code-Review-Bench problems. See docs/prds/pr-review-agent.md, Verification.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/prbench"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func main() { os.Exit(run()) }

func run() int {
	var opts prbench.Options
	var problems string
	flag.StringVar(&opts.Config, "config", configuration.DefaultConfigPath(), "configuration file whose providers serve the judge model")
	flag.StringVar(&opts.Fixtures, "findings", "", "directory of <problem id>.json findings to score")
	flag.StringVar(&opts.JudgeModel, "judge-model", "anthropic/claude-sonnet-4.6", "provider/model of the independent judge")
	flag.StringVar(&opts.Out, "out", "prbench-out", "directory for per-problem results and summary.json")
	flag.StringVar(&problems, "problems", "", "comma-separated problem ids (default: all)")
	flag.IntVar(&opts.Concurrency, "concurrency", 4, "problems judged at once")
	flag.Parse()
	if problems != "" {
		opts.Problems = strings.Split(problems, ",")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := prbench.Run(ctx, opts, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "archie-prbench:", err)
		return 1
	}
	return 0
}
