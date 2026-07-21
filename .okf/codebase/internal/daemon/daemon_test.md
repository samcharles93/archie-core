---
description: Source module internal/daemon/daemon_test.go (17 lines).
resource: internal/daemon/daemon_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: daemon_test.go
type: Module
---

# Module daemon_test.go

**Path**: `internal/daemon/daemon_test.go`  
**Lines**: 17

## Snippet Preview

```
package daemon

import "testing"

func TestHasLabelUsesExactNames(t *testing.T) {
	labels := []string{"bug", "archie:parked-old", "feature", "custom,label"}
	if hasLabel(labels, "archie:parked") {
		t.Fatal("hasLabel matched a label-name substring")
	}
	if !hasLabel(labels, "custom,label") {
		t.Fatal("hasLabel did not preserve a label containing a comma")
	}
	labels = append(labels, "archie:parked")
	if !hasLabel(labels, "archie:parked") {
		t.Fatal("hasLabel did not match the exact label name")
	}
}
```
