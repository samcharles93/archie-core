//go:build ignore

// log module implementation for archied's EDA playbook engine (Module
// position, t2db.13). The first action kind: side-effect-free -- it only
// writes a line to the daemon's log, so it proves the schema -> go:generate
// -> Yaegi load -> ModuleRegistry -> dispatch mechanism end to end without
// needing an idempotency answer.
//
// Install: copy this file into the configured module_dir (default
// ~/.config/archie/modules) and restart archied. The daemon evaluates it
// with Yaegi and registers kind "log". The //go:build ignore tag is a
// comment to the interpreter, so this file loads normally; the Go toolchain
// skips it, so it may live inside the module tree.
//
// Contract: package main, func Run(log.Args) log.Result -- resolved by name
// against logextract.Symbols, exactly as a custom gate resolves "gate.Check"
// against gateextract.Symbols. Args decode is strict: an unknown or
// wrong-typed field is a reported failure, not a silent zero fill.
package main

import (
	"github.com/samcharles93/archie-core/internal/domain/eda/module/log"
)

func Run(a log.Args) log.Result {
	level := a.Level
	if level == "" {
		level = "info"
	}
	return log.Result{
		Written: a.Message != "",
		Level:   level,
	}
}
