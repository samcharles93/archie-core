---
description: Source module internal/nats/client.go (176 lines).
resource: internal/nats/client.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: client.go
type: Module
---

# Module client.go

**Path**: `internal/nats/client.go`  
**Lines**: 176

## Snippet Preview

```
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	streamName   = "ARCHIE_TASKS"
	consumerName = "archie-daemon"
	msgPrefix    = "archie:" // for Nats-Msg-Id dedup
	dedupWindow  = 2 * time.Minute
	pollTimeout  = 2 * time.Second
)

// TaskMessage is the NATS payload for a discovered issue.
type TaskMessage struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels string `json:"labels"` // comma-separated
```
