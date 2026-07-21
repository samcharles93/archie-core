---
description: Source module internal/storage/storage.go (216 lines).
resource: internal/storage/storage.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: storage.go
type: Module
---

# Module storage.go

**Path**: `internal/storage/storage.go`  
**Lines**: 216

## Snippet Preview

```
// Package storage defines the pluggable storage backend interface for agent
// container execution. The Docker backend (DockerBackend) is the MVP
// implementation — no NFS/SMB/S3 yet, just Docker volumes + bind mounts.
//
// /data/ volume layout:
//
//	/data/
//	  task.json          — boot-time brief (written by container.WriteTaskJSON)
//	  worktree/          — bind mount of host worktree
//	  cache/
//	    go/              — GOMODCACHE (shared across all tasks)
//	    node/            — npm cache (shared)
//	    pnpm/            — pnpm store (shared)
//	    pip/             — pip cache (shared)
//	    cargo/           — Rust cargo cache (shared)
//	  plugins/           — skill plugins staged from worktree
//
// Cache volumes are named Docker volumes created once and shared across all
// tasks. They are never deleted — the daemon operator manages them.
package storage

import (
	"context"
	"fmt"
	"sort"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

```
