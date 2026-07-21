---
description: Source module internal/agentexec/subprocess.go (162 lines).
resource: internal/agentexec/subprocess.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: subprocess.go
type: Module
---

# Module subprocess.go

**Path**: `internal/agentexec/subprocess.go`  
**Lines**: 162

## Snippet Preview

```
package agentexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const maxDiagnosticBytes = 64 << 10

// SubprocessRunner executes each agent stage in a fresh archie-agent process.
type SubprocessRunner struct {
	Command string
	Args    []string
	// Environ is the daemon environment to select from. AdditionalEnv names
	// operator-approved compatibility variables; the requested provider's key
	// variable is added automatically for that invocation only.
	Environ       []string
	AdditionalEnv []string
	Diagnostics   io.Writer
	Providers     map[string]Provider
}

func (r *SubprocessRunner) Run(ctx context.Context, workspace string, req Request) (Result, error) {
	providerName, _, ok := strings.Cut(req.Model, "/")
```
