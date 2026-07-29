// Package skillscript runs skill-bundled Go scripts (.agents/skills/<skill>/scripts/*.go)
// via Yaegi. Unlike the gate and workflow-stage extension surfaces, these
// scripts only need the Go standard library  --  they wrap external tools
// (gitleaks, trivy, ...) the way a shell script would  --  so no
// archie-core-specific symbol table is required here.
package skillscript

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib/unrestricted"

	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// defaultTimeout protects callers that use Run (the legacy, context-free
// signature) from a script that blocks forever. Callers that have a
// context should use RunContext instead.
const defaultTimeout = 1 * time.Second

// Run interprets the Go script at path and executes its main function,
// returning everything it wrote to stdout and stderr. The script runs
// in-process (interpreted, not sandboxed) with unrestricted symbols
// (os/exec included  --  these scripts wrap external tools the way a shell
// script would), so a panic inside it is recovered and returned as an
// error rather than taking down the daemon.
//
// Run is a convenience wrapper around RunContext with a short built-in
// timeout. Callers that need precise cancellation or a different deadline
// should use RunContext directly.
func Run(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	return RunContext(ctx, path)
}

// RunContext interprets the Go script at path and executes its main
// function, returning everything it wrote to stdout and stderr.
// The script is executed in a goroutine; if ctx is cancelled or exceeds
// its deadline before the script finishes, RunContext returns the
// context error and abandons the goroutine (yaegi does not support
// external cancellation of an in-progress eval).
func RunContext(ctx context.Context, path string) (string, error) {
	if _, statErr := os.Stat(path); statErr != nil {
		return "", statErr
	}

	// Check context before spawning the goroutine.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	type result struct {
		output string
		err    error
	}

	ch := make(chan result, 1)
	go func() {
		out, err := yaegiutil.Safe(path, func() (string, error) {
			var buf bytes.Buffer
			i, err := yaegiutil.New(interp.Options{Stdout: &buf, Stderr: &buf}, unrestricted.Symbols)
			if err != nil {
				return "", err
			}

			// EvalPath treats a package-main source file the way `go run` does:
			// evaluating it executes its main() directly. Unlike `go build`, it
			// does not complain about a missing main()  --  such a script simply
			// produces no output.
			if _, err := i.EvalPath(path); err != nil {
				return "", fmt.Errorf("yaegi: run %s: %w", path, err)
			}
			return buf.String(), nil
		})
		ch <- result{out, err}
	}()

	select {
	case r := <-ch:
		return r.output, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
