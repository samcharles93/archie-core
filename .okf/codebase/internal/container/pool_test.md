---
description: Source module internal/container/pool_test.go (115 lines).
resource: internal/container/pool_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: pool_test.go
type: Module
---

# Module pool_test.go

**Path**: `internal/container/pool_test.go`  
**Lines**: 115

## Snippet Preview

```
package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── regression: Gap 2 — post-completion grace period ────────────────

func TestContainerSupportsGracePeriod(t *testing.T) {
	// Gap 2: MaxUptime is a container-level context timeout at creation,
	// not a post-completion grace period. PRD section 1 says:
	// "max_uptime — grace period after task completion before kill."
	//
	// The agent should stay alive after the task finishes so it can
	// handle follow-ups (gate re-runs, human replies). Currently
	// Release() stops the container immediately — there's no way to
	// keep it alive after task completion.

	c := &Container{ID: "test"}
	grace := 5 * time.Minute

	// A container must support being kept alive after task completion.
	// This means: after Release() is called (task done), the container
	// stays up for the grace period to handle follow-ups. Only after
	// the grace period expires (or on explicit shutdown) is it killed.
	//
```
