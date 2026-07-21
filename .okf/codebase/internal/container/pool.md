---
description: Source module internal/container/pool.go (258 lines).
resource: internal/container/pool.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: pool.go
type: Module
---

# Module pool.go

**Path**: `internal/container/pool.go`  
**Lines**: 258

## Snippet Preview

```
// Package container manages Docker containers running archie-agent.
// A Pool acquires and releases containers per task, handling image pull,
// creation, startup, health check, and teardown. The daemon's existing
// NATSRunner handles agent communication unchanged.
package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/storage"
)

// Container wraps a running Docker container.
type Container struct {
	ID string
}

// TaskPayload is the boot-time brief written to /data/task.json before
// the container starts, per PRD section 3.
```
