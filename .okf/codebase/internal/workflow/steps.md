---
description: Source module internal/workflow/steps.go (203 lines).
resource: internal/workflow/steps.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: steps.go
type: Module
---

# Module steps.go

**Path**: `internal/workflow/steps.go`  
**Lines**: 203

## Snippet Preview

```
package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/gate"
	"github.com/samcharles93/archie-core/internal/gate/gateeval"
	"github.com/samcharles93/archie-core/internal/store"
)

// Shared step library. Every workflow composes these; workflow-specific
// stages live next to their workflow definition.

// StagePrepareWorktree clones the repo fresh and checks out the task
// branch.
func StagePrepareWorktree() Stage {
	return Stage{Name: "prepare", Run: func(ctx context.Context, tc *TaskContext) error {
		dir, branch, err := tc.Trees.Prepare(ctx, tc.Task.Owner, tc.Task.Repo, tc.Repo.BaseBranch(), tc.Task.IssueNumber)
		if err != nil {
			return err
		}
		tc.Dir, tc.Branch = dir, branch
		tc.Task.Branch = branch
		return nil
	}}
```
