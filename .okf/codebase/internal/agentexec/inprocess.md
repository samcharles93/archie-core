---
description: Source module internal/agentexec/inprocess.go (229 lines).
resource: internal/agentexec/inprocess.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: inprocess.go
type: Module
---

# Module inprocess.go

**Path**: `internal/agentexec/inprocess.go`  
**Lines**: 229

## Snippet Preview

```
package agentexec

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"strings"

	"github.com/samcharles93/ai-sdk/agentloop"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/skillscript"
)

// Runner executes one autonomous stage against an already prepared workspace.
type Runner interface {
	Run(ctx context.Context, workspace string, req Request) (Result, error)
}

type loopFunc func(context.Context, agentloop.Config) (agentloop.Result, error)

// InProcessRunner preserves the current execution behavior behind the v1
// agent protocol. It is replaced by a subprocess or container runner without
// changing workflow orchestration.
type InProcessRunner struct {
	runtime *runtime.Runtime
```
