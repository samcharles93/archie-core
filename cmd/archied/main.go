// Command archied is the archie orchestrator daemon
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
	os.Exit(archied.Run())
}
