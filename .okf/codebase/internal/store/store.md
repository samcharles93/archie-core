---
description: Source module internal/store/store.go (286 lines).
resource: internal/store/store.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: store.go
type: Module
---

# Module store.go

**Path**: `internal/store/store.go`  
**Lines**: 286

## Snippet Preview

```
// Package store is archied's SQLite state: one row per task (a GitHub
// issue picked up for work), with every lifecycle transition recorded.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Task lifecycle statuses. Workflows move tasks between them; the
// daemon owns queued→running claims and crash recovery.
const (
	StatusQueued       = "queued"
	StatusRunning      = "running"
	StatusWaitingHuman = "waiting_human"
	StatusPROpen       = "pr_open"
	StatusMerged       = "merged"
	StatusParked       = "parked"
	StatusRejected     = "rejected"
	StatusClosedWontDo = "closed_wont_do"
)

type Task struct {
```
