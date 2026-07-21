---
description: Source module internal/agentexec/protocol.go (206 lines).
resource: internal/agentexec/protocol.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: protocol.go
type: Module
---

# Module protocol.go

**Path**: `internal/agentexec/protocol.go`  
**Lines**: 206

## Snippet Preview

```
// Package agentexec defines the unprivileged agent execution boundary.
// Workflow orchestration and external side effects remain daemon-owned.
package agentexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const ProtocolVersion = 1

// ErrBlocked is returned by ReviewResult when the daemon blocks agent output
// from reaching human channels.
var ErrBlocked = errors.New("agent output blocked by daemon review")

const StatusPassed = "passed"

// Command is one executable gate or preflight command.
type Command struct {
	Name          string   `json:"name"`
	Argv          []string `json:"argv"`
	ExpectFailure bool     `json:"expect_failure,omitempty"`
}

// Gate is the quality gate an agent must satisfy.
type Gate struct {
```
