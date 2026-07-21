---
description: Source module internal/worktree/worktree_test.go (138 lines).
resource: internal/worktree/worktree_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: worktree_test.go
type: Module
---

# Module worktree_test.go

**Path**: `internal/worktree/worktree_test.go`  
**Lines**: 138

## Snippet Preview

```
package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newLocalRemote creates a bare repo with one commit on main and
// returns the directory that serves as the fake forge host: the bare
// repo lives at <host>/<owner>/<repo>.git so cloneURL resolves it via
// file://.
func newLocalRemote(t *testing.T, owner, repo string) string {
	t.Helper()
	host := t.TempDir()
	bare := filepath.Join(host, owner, repo+".git")
	seed := filepath.Join(t.TempDir(), "seed")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
```
