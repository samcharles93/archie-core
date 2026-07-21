---
description: Source module internal/gate/gateeval/yaegi.go (67 lines).
resource: internal/gate/gateeval/yaegi.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: yaegi.go
type: Module
---

# Module yaegi.go

**Path**: `internal/gate/gateeval/yaegi.go`  
**Lines**: 67

## Snippet Preview

```
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
```
