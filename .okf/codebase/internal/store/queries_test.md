---
description: Source module internal/store/queries_test.go (224 lines).
resource: internal/store/queries_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: queries_test.go
type: Module
---

# Module queries_test.go

**Path**: `internal/store/queries_test.go`  
**Lines**: 224

## Snippet Preview

```
package store

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

func TestTaskByIssue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if got, err := s.TaskByIssue(ctx, "sam", "archie", 1); err != nil || got != nil {
		t.Fatalf("TaskByIssue on empty store = (%+v, %v)", got, err)
	}

	if _, err := s.EnqueueIssue(ctx, "sam", "archie", 1, "title", "body", "bug"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TaskByIssue(ctx, "sam", "archie", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", got, err)
	}
	if got.Title != "title" || got.IssueNumber != 1 {
		t.Fatalf("TaskByIssue = %+v", got)
	}

	if got, err := s.TaskByIssue(ctx, "sam", "archie", 2); err != nil || got != nil {
```
