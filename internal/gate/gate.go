// Package gate defines the runtime-evaluated custom gate surface: a
// per-repo .archie/gate.go, interpreted via Yaegi after the shell-based
// gate passes, that inspects the diff and worktree for project-specific
// rules shell commands can't express (AST checks, diff scanning, and
// the like).
package gate

import "fmt"

// Level is the severity of a gate finding. LevelError blocks the gate;
// LevelWarn is advisory and logged only.
type Level string

const (
	// LevelError blocks the gate: the task is parked instead of opening a PR.
	LevelError Level = "error"
	// LevelWarn is advisory and logged only.
	LevelWarn Level = "warn"
)

// GateContext carries everything a custom gate function can inspect.
type GateContext struct {
	// Diff is the unified diff of all changes against BaseRef.
	Diff string
	// ChangedFiles lists changed file paths, repo-relative.
	ChangedFiles []string
	// Dir is the absolute worktree path.
	Dir string
	// BaseRef is the base branch the diff is against (e.g. "origin/main").
	BaseRef string
	// Repo is "owner/name".
	Repo string
}

// Finding is one gate violation.
type Finding struct {
	// Level is the severity: LevelError blocks the gate, LevelWarn is
	// advisory and logged only.
	Level Level
	// File is the optional file path the finding applies to.
	File string
	// Line is the optional line number within File.
	Line    int
	Message string
}

// Blocking reports whether this finding blocks the gate.
func (f Finding) Blocking() bool {
	return f.Level == LevelError
}

// Blocking reports whether any finding blocks the gate.
func Blocking(findings []Finding) bool {
	for _, f := range findings {
		if f.Blocking() {
			return true
		}
	}
	return false
}

// Validate checks that the finding satisfies contract invariants. An
// unrecognized level fails closed: a script that emits a mis-typed or empty
// level is an error, never a silently non-blocking finding.
func (f Finding) Validate() error {
	switch f.Level {
	case LevelError, LevelWarn:
		return nil
	default:
		return fmt.Errorf("invalid gate level %q (must be %q or %q)", f.Level, LevelError, LevelWarn)
	}
}
