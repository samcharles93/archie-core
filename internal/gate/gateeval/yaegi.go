// Package gateeval evaluates a repo's .archie/gate.go via Yaegi. Split
// from internal/gate (which only declares the GateContext/Finding types)
// because the generated symbol table in gateextract imports internal/gate  -- 
// this package sits above both to avoid the resulting import cycle.
package gateeval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/gate"
	"github.com/samcharles93/archie-core/internal/gate/gateextract"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// scriptPath is the per-repo custom gate script, relative to the
// worktree root.
const scriptPath = ".archie/gate.go"

// Evaluate loads and runs the repo's .archie/gate.go against gctx. A
// missing script is not an error  --  it returns (nil, nil), meaning no
// custom gate is configured for this repo. The script runs in-process
// (interpreted, not sandboxed), so a panic inside it  --  nil dereference,
// out-of-range index  --  is recovered and returned as an error rather than
// taking down the daemon.
func Evaluate(gctx gate.GateContext) ([]gate.Finding, error) {
	src, err := os.ReadFile(filepath.Join(gctx.Dir, scriptPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", scriptPath, err)
	}

	return yaegiutil.Safe(scriptPath, func() ([]gate.Finding, error) {
		i, err := yaegiutil.New(interp.Options{}, gateextract.Symbols)
		if err != nil {
			return nil, err
		}
		check, err := yaegiutil.Resolve[func(gate.GateContext) []gate.Finding](i, string(src), "gate.Check")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", scriptPath, err)
		}
		return check(gctx), nil
	})
}
