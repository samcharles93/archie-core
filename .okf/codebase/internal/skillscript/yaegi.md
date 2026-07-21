---
description: Source module internal/skillscript/yaegi.go (53 lines).
resource: internal/skillscript/yaegi.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: yaegi.go
type: Module
---

# Module yaegi.go

**Path**: `internal/skillscript/yaegi.go`  
**Lines**: 53

## Snippet Preview

```
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
```
