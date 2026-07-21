---
description: Source module internal/agentexec/skill_test.go (94 lines).
resource: internal/agentexec/skill_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: skill_test.go
type: Module
---

# Module skill_test.go

**Path**: `internal/agentexec/skill_test.go`  
**Lines**: 94

## Snippet Preview

```
package agentexec

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// captureRunner records the Request it receives for test assertions.
type captureRunner struct {
	callback func(Request)
}

func (c *captureRunner) Run(_ context.Context, _ string, req Request) (Result, error) {
	c.callback(req)
	return Result{
		Version:    ProtocolVersion,
		TaskID:     req.TaskID,
		Attempt:    req.Attempt,
		Stage:      req.Stage,
		Status:     StatusPassed,
		Summary:    "done",
		TokensUsed: 1,
	}, nil
}

// ── regression: Agent doesn't load its own skills ───────────────────

```
