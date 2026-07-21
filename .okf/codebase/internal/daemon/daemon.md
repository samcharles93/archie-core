---
description: Source module internal/daemon/daemon.go (577 lines).
resource: internal/daemon/daemon.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: daemon.go
type: Module
---

# Module daemon.go

**Path**: `internal/daemon/daemon.go`  
**Lines**: 577

## Snippet Preview

```
// Package daemon is archied's resident loop: poll GitHub for labelled
// issues, enqueue them, and process one task at a time through its
// routed workflow. State lives in the store; the daemon is restartable
// at any point.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	arnats "github.com/samcharles93/archie-core/internal/nats"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/workflow"
```
