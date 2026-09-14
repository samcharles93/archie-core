// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package main

import (
	"os"

	"github.com/samcharles93/archie-core/internal/app/archied"
)

func main() {
	args := os.Args[1:]
	// The setup generator is dispatched before the daemon's flags are
	// parsed: it writes a first-run config.toml and must not touch the
	// store, NATS, or the running daemon's configuration.
	if archied.IsSetupArgs(args) {
		os.Exit(archied.RunSetup(args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	// The codesearch helper is dispatched before the daemon's flags are
	// parsed: it is this same binary re-invoked as a short-lived child by
	// internal/indexing, and it must not touch config, the store or NATS.
	if archied.IsCodesearchHelperArgs(args) {
		os.Exit(archied.RunCodesearchHelper(args[1:], os.Stdout))
	}
	os.Exit(archied.Run())
}
