// Package skillscript runs skill-bundled Go scripts (.archie/skills/<skill>/scripts/*.go)
// via Yaegi. Unlike the gate and workflow-stage extension surfaces, these
// scripts only need the Go standard library — they wrap external tools
// (gitleaks, trivy, ...) the way a shell script would — so no
// archie-core-specific symbol table is required here.
package skillscript

import (
	"bytes"
	"fmt"
	"os"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"github.com/traefik/yaegi/stdlib/unrestricted"
)

// Run interprets the Go script at path and executes its main function,
// returning everything it wrote to stdout and stderr. The script runs
// in-process (interpreted, not sandboxed) with unrestricted symbols
// (os/exec included — these scripts wrap external tools the way a shell
// script would), so a panic inside it is recovered and returned as an
// error rather than taking down the daemon.
func Run(path string) (output string, err error) {
	if _, statErr := os.Stat(path); statErr != nil {
		return "", statErr
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("yaegi: %s panicked: %v", path, r)
		}
	}()

	var buf bytes.Buffer
	i := interp.New(interp.Options{Stdout: &buf, Stderr: &buf})
	if err := i.Use(stdlib.Symbols); err != nil {
		return "", fmt.Errorf("yaegi: load stdlib symbols: %w", err)
	}
	if err := i.Use(unrestricted.Symbols); err != nil {
		return "", fmt.Errorf("yaegi: load unrestricted symbols: %w", err)
	}

	// EvalPath treats a package-main source file the way `go run` does:
	// evaluating it executes its main() directly. Unlike `go build`, it
	// does not complain about a missing main() — such a script simply
	// produces no output.
	if _, err := i.EvalPath(path); err != nil {
		return "", fmt.Errorf("yaegi: run %s: %w", path, err)
	}

	return buf.String(), nil
}
