// Command contractaudit reports contract-conformance state across Archie's
// declared surfaces: which capabilities have a consumer, which have an
// explicit declaration that they do not, and which have neither.
//
// It is the sibling of tools/reachaudit. Reachability asks "is this code in a
// shipped binary?"; conformance asks "does every declared surface have a
// consumer, or a declaration that it has none?". A capability can be reachable
// and unconsumed -- ratelimit was in the binary closure and called by nothing
// (archie-core-6wxb) -- which reachability cannot see and this can.
//
// Advisory by default: it prints, it never deletes or rewrites anything. Pass
// -strict to fail on an undeclared or stale entry.
//
// See docs/prds/contract-conformance-audit.md.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	repoRoot := flag.String("repo-root", "..", "path to the Archie repository root")
	declPath := flag.String(
		"declarations",
		"docs/contract-declarations.json",
		"declaration allowlist, relative to the repository root",
	)
	strict := flag.Bool("strict", false, "fail on undeclared or stale entries")
	jsonOut := flag.Bool("json", false, "emit the report as JSON instead of text")
	flag.Parse()

	if err := run(*repoRoot, *declPath, *strict, *jsonOut, os.Stdout); err != nil {
		log.Fatalf("contractaudit: %v", err)
	}
}

func run(repoRoot, declPath string, strict, jsonOut bool, out *os.File) error {
	audit, err := New(repoRoot, declPath)
	if err != nil {
		return err
	}
	findings, err := audit.Run()
	if err != nil {
		return err
	}
	if jsonOut {
		if err := writeJSON(out, findings); err != nil {
			return err
		}
	} else {
		writeText(out, findings)
	}
	if strict && UndeclaredCount(findings) > 0 {
		return fmt.Errorf(
			"%d undeclared or stale contract entries; declare them in %s or consume them",
			UndeclaredCount(findings), declPath,
		)
	}
	return nil
}
