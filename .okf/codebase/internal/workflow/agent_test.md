---
description: Source module internal/workflow/agent_test.go (276 lines).
resource: internal/workflow/agent_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: agent_test.go
type: Module
---

# Module agent_test.go

**Path**: `internal/workflow/agent_test.go`  
**Lines**: 276

## Snippet Preview

```
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
)

type fakeAgentRunner struct {
	workspace string
	request   agentexec.Request
	result    agentexec.Result
	err       error
}

type agentRunnerFunc func(context.Context, string, agentexec.Request) (agentexec.Result, error)

func (f agentRunnerFunc) Run(ctx context.Context, workspace string, req agentexec.Request) (agentexec.Result, error) {
	return f(ctx, workspace, req)
}

func (r *fakeAgentRunner) Run(_ context.Context, workspace string, req agentexec.Request) (agentexec.Result, error) {
```
