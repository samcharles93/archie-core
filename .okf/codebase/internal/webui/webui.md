---
description: Source module internal/webui/webui.go (177 lines).
resource: internal/webui/webui.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: webui.go
type: Module
---

# Module webui.go

**Path**: `internal/webui/webui.go`  
**Lines**: 177

## Snippet Preview

```
// Package webui is archied's observability dashboard: task board,
// per-task timelines, workflow/stage metrics, and a live event tail
// over SSE. Read-only — the daemon is steered through GitHub, not here.
// Bind it to localhost or a tailnet address; it has no auth.
package webui

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

//go:embed index.html
var indexHTML []byte

type Server struct {
	Store *store.Store
	Log   *slog.Logger

	mu    sync.Mutex
	conns map[chan events.Event]struct{}
}
```
