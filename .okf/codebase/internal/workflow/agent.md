---
description: Source module internal/workflow/agent.go (221 lines).
resource: internal/workflow/agent.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: agent.go
type: Module
---

# Module agent.go

**Path**: `internal/workflow/agent.go`  
**Lines**: 221

## Snippet Preview

```
package workflow

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/skill"
)

// AgentStage is the reusable bridge from a workflow stage to an
// agentloop run. Every LLM-driven stage in any workflow (implement's
// planner/builder, tdd's test-writer/fixer, feasibility's analyst)
// is an AgentStage with a different mission, gate, and result handler —
// never a new engine.
type AgentStage struct {
	Name string
	// Role selects the model via cfg.Models[Role]; falls back to
	// cfg.Models["builder"].
	Role     string
	ReadOnly bool
	// Mission produces the task statement from the current context.
	Mission func(*TaskContext) string
	// Gate returns the stage's quality gate; nil means ungated (e.g.
	// read-only analysis stages).
	Gate func(*TaskContext) agentexec.Gate
	// ExtraRules is appended to the system prompt.
	ExtraRules string
	// MaxSteps overrides the configured step budget when > 0 (planner
```
