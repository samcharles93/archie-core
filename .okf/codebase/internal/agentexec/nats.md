---
description: Source module internal/agentexec/nats.go (115 lines).
resource: internal/agentexec/nats.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: nats.go
type: Module
---

# Module nats.go

**Path**: `internal/agentexec/nats.go`  
**Lines**: 115

## Snippet Preview

```
package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	natsio "github.com/nats-io/nats.go"

	arnats "github.com/samcharles93/archie-core/internal/nats"
)

// AgentRequestMessage is the NATS payload for an agent stage execution request.
// When Workflow is set and Stages is populated, the agent runs all stages as a
// per-task batch rather than a single stage. PRD §1.
type AgentRequestMessage struct {
	TaskID    int64             `json:"task_id"`
	Attempt   int               `json:"attempt"`
	Stage     string            `json:"stage"`
	Workflow  string            `json:"workflow,omitempty"`
	Channel   string            `json:"channel,omitempty"` // "response" (default) or "system"
	Workspace string            `json:"workspace"`
	Request   Request           `json:"request"`
	Stages    []Request         `json:"stages,omitempty"` // batch: all agent stages for this task
	Providers map[string]Provider `json:"providers,omitempty"`
}

```
