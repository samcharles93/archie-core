---
description: Source module internal/agentexec/nats_test.go (291 lines).
resource: internal/agentexec/nats_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: nats_test.go
type: Module
---

# Module nats_test.go

**Path**: `internal/agentexec/nats_test.go`  
**Lines**: 291

## Snippet Preview

```
package agentexec

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	arnats "github.com/samcharles93/archie-core/internal/nats"
)

// ── helpers ──────────────────────────────────────────────────────────

type mockRunner struct{}

func (m *mockRunner) Run(_ context.Context, _ string, req Request) (Result, error) {
	return Result{
		Version:    ProtocolVersion,
		TaskID:     req.TaskID,
		Attempt:    req.Attempt,
		Stage:      req.Stage,
		Status:     StatusPassed,
		Summary:    "mock:" + req.Mission,
		TokensUsed: 10,
```
