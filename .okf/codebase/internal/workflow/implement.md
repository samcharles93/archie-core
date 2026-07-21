---
description: Source module internal/workflow/implement.go (111 lines).
resource: internal/workflow/implement.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: implement.go
type: Module
---

# Module implement.go

**Path**: `internal/workflow/implement.go`  
**Lines**: 111

## Snippet Preview

```
package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// StageBaselineGate verifies the repo's gate is green at the base commit
// before any planning starts — a red baseline would park-storm the
// builder with failures it didn't cause.
func StageBaselineGate() Stage {
	return Stage{Name: "baseline", Run: func(ctx context.Context, tc *TaskContext) error {
		for _, argv := range tc.Repo.Gate {
			if len(argv) == 0 {
				continue
			}
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = tc.Dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("baseline red — %s fails at %s before archie touched anything:\n%s",
					strings.Join(argv, " "), tc.Repo.BaseBranch(), clip(string(out), 2000))
			}
		}
		return nil
	}}
}
```
