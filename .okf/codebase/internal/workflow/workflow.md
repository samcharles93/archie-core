---
description: Source module internal/workflow/workflow.go (244 lines).
resource: internal/workflow/workflow.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: workflow.go
type: Module
---

# Module workflow.go

**Path**: `internal/workflow/workflow.go`  
**Lines**: 244

## Snippet Preview

```
// Package workflow is archied's extensible pipeline engine. A Workflow
// is an ordered list of stages over a shared TaskContext; stages are
// either deterministic steps (git, gate, PR, comments) or agent stages
// (agentloop runs). New workflows compose from the shared step library —
// adding one must never require reimplementing the engine or the steps.
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// TaskContext carries everything a stage may need. Stages communicate
// forward by mutating Task (persisted after every stage) and the
// scratch fields below.
type TaskContext struct {
	Task  *store.Task
	Repo  config.Repo
	Cfg   config.Config
```
