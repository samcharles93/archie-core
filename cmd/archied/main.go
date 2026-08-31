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
	// The codesearch helper is dispatched before the daemon's flags are
	// parsed: it is this same binary re-invoked as a short-lived child by
	// internal/indexing, and it must not touch config, the store or NATS.
	if args := os.Args[1:]; archied.IsCodesearchHelperArgs(args) {
		os.Exit(archied.RunCodesearchHelper(args[1:], os.Stdout))
	}
	os.Exit(archied.Run())
}
