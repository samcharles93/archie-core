---
description: Source module internal/workflow/tdd_test.go (47 lines).
resource: internal/workflow/tdd_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: tdd_test.go
type: Module
---

# Module tdd_test.go

**Path**: `internal/workflow/tdd_test.go`  
**Lines**: 47

## Snippet Preview

```
package workflow

import (
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestTDDGateUsesLastNonEmptyCommand(t *testing.T) {
	repo := config.Repo{Gate: [][]string{
		{"go", "vet", "./..."},
		{"go", "test", "./..."},
		{},
	}}

	gate := tddReproGate(repo, config.Budgets{GateMaxFailures: 3})
	if len(gate.Commands) != 2 {
		t.Fatalf("gate has %d commands, want 2", len(gate.Commands))
	}
	if gate.Commands[0].ExpectFailure {
		t.Error("first gate command unexpectedly requires failure")
	}
	if !gate.Commands[1].ExpectFailure {
		t.Error("last non-empty gate command does not require failure")
	}
	if got, want := testCommandArgv(repo), []string{"go", "test", "./..."}; !reflect.DeepEqual(got, want) {
		t.Errorf("testCommandArgv() = %v, want %v", got, want)
	}
	if got, want := testCommand(repo), "go test ./..."; got != want {
```
