// Package enginehost provides host-side helpers for interpreted secret
// engine plugins.
//
// Yaegi's interpreted os/exec is sandboxed: child processes cannot be
// given an environment, and env vars set or read in interpreted code do
// not reach the host or the child. Secret engines need real command
// execution with the daemon's environment (vault requires VAULT_ADDR and
// VAULT_TOKEN in the child env), so plugins shell out through Run instead
// of importing os/exec. This mirrors how the compiled-in bws engine runs
// the bws CLI, but host-side where env behaves.
package enginehost

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"time"
)

// runTimeout bounds a CLI invocation, mirroring the compiled-in bws
// engine's bwsTimeout: a hung engine binary must not block secret
// resolution forever.
const runTimeout = 30 * time.Second

// Run executes name with args in the host process, inheriting the full
// host environment, and returns the trimmed stdout. Stderr is discarded,
// matching the compiled-in bws engine. A non-zero exit is returned as an
// error. The engine Resolve contract carries no context, so the command
// runs with a bounded timeout rather than a caller-provided context.
func Run(name string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run %s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Symbols is the hand-written Yaegi symbol table exposing Run to
// interpreted plugins. It is kept separate from the generated
// secretextract table because regenerating that table would not include
// this package. The key follows yaegi's convention of import path plus
// package name (see the generated secretextract file).
var Symbols = map[string]map[string]reflect.Value{
	"github.com/samcharles93/archie-core/internal/secret/enginehost/enginehost": {
		"Run": reflect.ValueOf(Run),
	},
}
