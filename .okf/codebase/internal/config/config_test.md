---
description: Source module internal/config/config_test.go (287 lines).
resource: internal/config/config_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: config_test.go
type: Module
---

# Module config_test.go

**Path**: `internal/config/config_test.go`  
**Lines**: 287

## Snippet Preview

```
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchDefaults(t *testing.T) {
	// Simulate a config with no [dispatch] section — all fields zero.
	var d Dispatch
	d.Labels = map[string]string{}

	// Per-key defaults (mimicking Load's loop).
	for k, v := range dispatchLabelDefaults {
		if d.Labels[k] == "" {
			d.Labels[k] = v
		}
	}
	if d.Trigger == "" {
		d.Trigger = "assignee"
	}
	if d.AckReaction == "" {
		d.AckReaction = "eyes"
	}

	// StateLabel should return the legacy constants.
	if got := d.StateLabel("queued"); got != "archie:queued" {
		t.Errorf("queued: got %q, want archie:queued", got)
```
