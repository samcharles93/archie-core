// Command archied is the archie orchestrator daemon
package main

import (
	"os"

	"github.com/samcharles93/archie-core/internal/app/archied"
	"github.com/samcharles93/archie-core/internal/app/archieplaybooks"
	"github.com/samcharles93/archie-core/internal/app/prbench"
)

func main() {
	args := os.Args[1:]
	// The setup generator is dispatched before the daemon's flags are
	// parsed: it writes a first-run config.toml and must not touch the
	// store, NATS, or the running daemon's configuration.
	if archied.IsSetupArgs(args) {
		os.Exit(archied.RunSetup(args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(args) > 0 && args[0] == "update" {
		os.Exit(archied.RunUpdate(args[1:], os.Stdout, os.Stderr))
	}
	if len(args) > 0 && args[0] == "update-image" {
		os.Exit(archied.RunUpdateImage(args[1:], os.Stderr))
	}
	if len(args) > 0 && args[0] == "update-topology" {
		os.Exit(archied.RunUpdateTopology(args[1:], os.Stdout, os.Stderr))
	}
	if len(args) > 0 && args[0] == "status" {
		os.Exit(archied.RunStatus(args[1:], os.Stdout, os.Stderr))
	}
	if len(args) > 0 && args[0] == "prbench" {
		os.Exit(prbench.Main(args[1:], os.Stdout, os.Stderr))
	}
	if len(args) > 0 && args[0] == "playbooks" {
		os.Exit(archieplaybooks.Run(args[1:], os.Stderr))
	}
	os.Exit(archied.Run())
}
