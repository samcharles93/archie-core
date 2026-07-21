---
description: Source module internal/storage/storage_test.go (389 lines).
resource: internal/storage/storage_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: storage_test.go
type: Module
---

# Module storage_test.go

**Path**: `internal/storage/storage_test.go`  
**Lines**: 389

## Snippet Preview

```
package storage

import (
	"context"
	"os"
	"sort"
	"testing"
)

// ── interface contract ───────────────────────────────────────────────

func TestBackendInterfaceIsSatisfied(t *testing.T) {
	// Compile-time check that DockerBackend satisfies Backend.
	var _ Backend = (*DockerBackend)(nil)
}

func TestTaskRefFields(t *testing.T) {
	ref := TaskRef{
		Owner:       "alice",
		Repo:        "demo",
		IssueNumber: 42,
		Ecosystem:   "go",
		WorktreeDir: "/tmp/worktree",
	}
	if ref.Owner != "alice" || ref.Repo != "demo" || ref.IssueNumber != 42 {
		t.Fatal("TaskRef fields not assignable")
	}
}

func TestMountStruct(t *testing.T) {
```
