---
description: Source module cmd/archie-agent/main.go (177 lines).
resource: cmd/archie-agent/main.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: main.go
type: Module
---

# Module main.go

**Path**: `cmd/archie-agent/main.go`  
**Lines**: 177

## Snippet Preview

```
// Command archie-agent is a long-running NATS-connected worker that executes
// autonomous agent stages. It subscribes to archie.agent.> on the ARCHIE_TASKS
// JetStream stream, runs the agent loop for each received request, and publishes
// the result back to the daemon's reply inbox.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/agentexec"
	arnats "github.com/samcharles93/archie-core/internal/nats"
)

const (
	streamName   = "ARCHIE_TASKS"
	consumerName = "archie-agent"
	dedupWindow  = 2 * time.Minute
	pollTimeout  = 5 * time.Second
	ackWait      = 30 * time.Minute
```
