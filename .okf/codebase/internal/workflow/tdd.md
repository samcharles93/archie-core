---
description: Source module internal/workflow/tdd.go (195 lines).
resource: internal/workflow/tdd.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: tdd.go
type: Module
---

# Module tdd.go

**Path**: `internal/workflow/tdd.go`  
**Lines**: 195

## Snippet Preview

```
package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
)

// TDD is the bugfix workflow: prove the bug with failing tests before
// fixing it. The repro stage's gate INVERTS the test command
// (ExpectFailure) — the run cannot proceed until the new tests fail for
// the right reason — and the fix stage restores the repo's full gate.
// Routed via the "bug" label.
func TDD() Workflow {
	return Workflow{
		Name: "tdd",
		Stages: []Stage{
			StagePrepareWorktree(),
			StageRepoStages(),
			StageBaselineGate(),

			AgentStage{
				Name:     "analyse",
				Role:     "planner",
				ReadOnly: true,
				MaxSteps: 15,
```
