---
description: Source module internal/store/store_test.go (114 lines).
resource: internal/store/store_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: store_test.go
type: Module
---

# Module store_test.go

**Path**: `internal/store/store_test.go`  
**Lines**: 114

## Snippet Preview

```
package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func TestEnqueueIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	ins, err := s.EnqueueIssue(ctx, "sam", "todo", 1, "add tests", "body", "archie")
	if err != nil || !ins {
```
