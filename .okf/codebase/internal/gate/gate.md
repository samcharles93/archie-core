---
description: Source module internal/gate/gate.go (41 lines).
resource: internal/gate/gate.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: gate.go
type: Module
---

# Module gate.go

**Path**: `internal/gate/gate.go`  
**Lines**: 41

## Snippet Preview

```
// Package gate defines the runtime-evaluated custom gate surface: a
// per-repo .archie/gate.go, interpreted via Yaegi after the shell-based
// gate passes, that inspects the diff and worktree for project-specific
// rules shell commands can't express (AST checks, diff scanning, and
// the like).
package gate

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
	// Level is "error" (blocks the gate) or "warn" (advisory, logged only).
	Level string
	// File is the optional file path the finding applies to.
	File string
	// Line is the optional line number within File.
	Line    int
	Message string
```
