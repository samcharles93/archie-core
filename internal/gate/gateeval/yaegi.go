// Package gateeval evaluates a repo's .archie/gate.go via Yaegi. Split
// from internal/gate (which only declares the GateContext/Finding types)
// because the generated symbol table in gateextract imports internal/gate —
// this package sits above both to avoid the resulting import cycle.
package gateeval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/samcharles93/archie-core/internal/gate"
	"github.com/samcharles93/archie-core/internal/gate/gateextract"
)

// scriptPath is the per-repo custom gate script, relative to the
// worktree root.
const scriptPath = ".archie/gate.go"

// Evaluate loads and runs the repo's .archie/gate.go against gctx. A
// missing script is not an error — it returns (nil, nil), meaning no
// custom gate is configured for this repo. The script runs in-process
// (interpreted, not sandboxed), so a panic inside it — nil dereference,
// out-of-range index — is recovered and returned as an error rather than
// taking down the daemon.
func Evaluate(gctx gate.GateContext) (findings []gate.Finding, err error) {
	src, err := os.ReadFile(filepath.Join(gctx.Dir, scriptPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", scriptPath, err)
	}

	defer func() {
		if r := recover(); r != nil {
			findings, err = nil, fmt.Errorf("yaegi: %s panicked: %v", scriptPath, r)
		}
	}()

	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		return nil, fmt.Errorf("yaegi: load stdlib symbols: %w", err)
	}
	if err := i.Use(gateextract.Symbols); err != nil {
		return nil, fmt.Errorf("yaegi: load gate symbols: %w", err)
	}

	if _, err := i.Eval(string(src)); err != nil {
		return nil, fmt.Errorf("yaegi: evaluate %s: %w", scriptPath, err)
	}

	v, err := i.Eval("gate.Check")
	if err != nil {
		return nil, fmt.Errorf("yaegi: %s does not export func Check(GateContext) []Finding: %w", scriptPath, err)
	}
	check, ok := v.Interface().(func(gate.GateContext) []gate.Finding)
	if !ok {
		return nil, fmt.Errorf("yaegi: %s Check has the wrong signature (want func(gate.GateContext) []gate.Finding)", scriptPath)
	}

	return check(gctx), nil
}
