---
description: Source module internal/events/events.go (128 lines).
resource: internal/events/events.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: events.go
type: Module
---

# Module events.go

**Path**: `internal/events/events.go`  
**Lines**: 128

## Snippet Preview

```
// Package events is archied's in-process observability stream, adapted
// from tau's eventbus but slimmed for a small daemon: one event type,
// mutex fan-out instead of a router goroutine, and — unlike tau's
// blocking delivery — bounded per-subscriber buffers that DROP on
// overflow, so a stalled dashboard connection can never apply
// backpressure to the task engine.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Kind values. Data carries kind-specific fields.
const (
	KindTaskQueued  = "task_queued"
	KindStageStart  = "stage_start"
	KindStageFinish = "stage_finish" // data: duration_ms, error
	KindAgentFinish = "agent_finish" // data: status, stop_reason, tokens, iterations, model
	KindParked      = "parked"       // data: reason
	KindOutcome     = "outcome"      // data: status, detail
	KindPRMerged    = "pr_merged"
	KindPRRejected  = "pr_rejected"
	KindLog         = "log" // data: level, msg
)

// Event is the single wire type: task lifecycle, stage progress, agent
// stats, and log lines all flow through it — every consumer (SQLite
// sink, SSE fan-out, future aggregators) is just another subscriber.
```
