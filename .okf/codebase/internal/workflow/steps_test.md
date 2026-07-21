---
description: Source module internal/workflow/steps_test.go (201 lines).
resource: internal/workflow/steps_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: steps_test.go
type: Module
---

# Module steps_test.go

**Path**: `internal/workflow/steps_test.go`  
**Lines**: 201

## Snippet Preview

```
package workflow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepoWithOriginRef creates a repo with a base commit, then labels
// that commit "origin/<base>" (a plain local branch standing in for a
// remote-tracking ref) so Manager.Diff/ChangedFiles — which always diff
```
