---
description: Source module internal/worktree/worktree.go (176 lines).
resource: internal/worktree/worktree.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: worktree.go
type: Module
---

# Module worktree.go

**Path**: `internal/worktree/worktree.go`  
**Lines**: 176

## Snippet Preview

```
// Package worktree owns archied's git operations: fresh clone per task,
// branch, commit as the bot identity, push, diff stats, cleanup. The
// daemon performs these deterministically — the model's shell tool never
// drives git.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Manager struct {
	// WorkDir is the root under which task worktrees are created.
	WorkDir string
	// Token authenticates clone/push over HTTPS via an askpass helper,
	// keeping it out of .git/config and process argv.
	Token    string
	BotUser  string
	BotEmail string
	// BaseURL overrides the forge host. Set from config [forge].host.
	// Empty falls back to https://github.com.
	BaseURL string
}

// Dir is the worktree path for a task.
func (m *Manager) Dir(owner, repo string, issue int) string {
```
