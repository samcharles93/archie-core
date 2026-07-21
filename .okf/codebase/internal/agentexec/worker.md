---
description: Source module internal/agentexec/worker.go (185 lines).
resource: internal/agentexec/worker.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: worker.go
type: Module
---

# Module worker.go

**Path**: `internal/agentexec/worker.go`  
**Lines**: 185

## Snippet Preview

```
package agentexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/samcharles93/archie-core/internal/skill"
)

const maxProtocolBytes = 8 << 20

// DefaultRunnerFactory creates an InProcessRunner backed by the ai-sdk runtime.
func DefaultRunnerFactory(providers map[string]Provider, log *slog.Logger) Runner {
	return NewInProcessRunner(NewRuntime(providers), log)
}

// ServeOne decodes and executes exactly one invocation.
func ServeOne(ctx context.Context, in io.Reader, out io.Writer, log *slog.Logger) error {
	return serveOne(ctx, in, out, func(invocation Invocation) Runner {
		return DefaultRunnerFactory(invocation.Providers, log)
	})
}

func serveOne(ctx context.Context, in io.Reader, out io.Writer, newRunner func(Invocation) Runner) error {
	var invocation Invocation
```
