// Package skillscript runs skill-bundled Go scripts (.agents/skills/<skill>/scripts/*.go)
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
	"github.com/traefik/yaegi/stdlib/unrestricted"

	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// Run interprets the Go script at path and executes its main function,
// returning everything it wrote to stdout and stderr. The script runs
// in-process (interpreted, not sandboxed) with unrestricted symbols
// (os/exec included — these scripts wrap external tools the way a shell
// script would), so a panic inside it is recovered and returned as an
// error rather than taking down the daemon.
func Run(path string) (string, error) {
	if _, statErr := os.Stat(path); statErr != nil {
		return "", statErr
	}

	return yaegiutil.Safe(path, func() (string, error) {
		var buf bytes.Buffer
		i, err := yaegiutil.New(interp.Options{Stdout: &buf, Stderr: &buf}, unrestricted.Symbols)
		if err != nil {
			return "", err
		}

		// EvalPath treats a package-main source file the way `go run` does:
		// evaluating it executes its main() directly. Unlike `go build`, it
		// does not complain about a missing main() — such a script simply
		// produces no output.
		if _, err := i.EvalPath(path); err != nil {
			return "", fmt.Errorf("yaegi: run %s: %w", path, err)
		}
		return buf.String(), nil
	})
}
