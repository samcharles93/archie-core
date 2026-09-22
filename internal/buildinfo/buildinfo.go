// Package buildinfo carries the release version stamped into every host
// binary. The updater asks each installed binary what it is, rather than
// trusting one component's word for a whole install whose binaries may have
// been replaced by hand (archie-core-k94o).
package buildinfo

import (
	"flag"
	"fmt"
	"os"
)

// Version and Runtime are stamped per release with
// -X github.com/samcharles93/archie-core/internal/buildinfo.Version=<version>.
// The unstamped value is "dev", which is itself informative: a binary built
// outside a release says so rather than guessing.
var (
	Version = "dev"
	Runtime = "dev"
)

// Print writes the machine-readable "name version" line the updater reads.
func Print(name string) {
	fmt.Printf("%s %s\n", name, Version)
}

// RegisterVersionFlag adds -version to the standard flag set. Call the returned
// function after flag.Parse to print and exit when it was set.
func RegisterVersionFlag(name string) func() {
	show := flag.Bool("version", false, "print the version and exit")
	return func() {
		if *show {
			Print(name)
			os.Exit(0)
		}
	}
}
