---
description: Source module internal/events/events_test.go (46 lines).
resource: internal/events/events_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: events_test.go
type: Module
---

# Module events_test.go

**Path**: `internal/events/events_test.go`  
**Lines**: 46

## Snippet Preview

```
package events

import "testing"

func TestBusFanOutAndDrop(t *testing.T) {
	b := NewBus()
	defer b.Close()

	fast := b.Subscribe(8)
	slow := b.Subscribe(2) // will overflow

	for i := range 5 {
		b.Publish(Event{Kind: KindLog, Issue: i})
	}

	if got := len(fast.C); got != 5 {
		t.Fatalf("fast subscriber buffered %d, want 5", got)
	}
	if got := len(slow.C); got != 2 {
		t.Fatalf("slow subscriber buffered %d, want 2", got)
	}
	if d := slow.Dropped(); d != 3 {
		t.Fatalf("slow.Dropped() = %d, want 3", d)
	}

	e := <-fast.C
	if e.At.IsZero() {
		t.Fatal("Publish must stamp At")
	}

```
