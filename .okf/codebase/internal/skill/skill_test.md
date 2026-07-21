---
description: Source module internal/skill/skill_test.go (164 lines).
resource: internal/skill/skill_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: skill_test.go
type: Module
---

# Module skill_test.go

**Path**: `internal/skill/skill_test.go`  
**Lines**: 164

## Snippet Preview

```
package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	src := []byte(`---
name: tdd-bugfix
description: Fix bugs with TDD
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint]
    engine: any
---

## Stage 1: Analyse

Read the code and find the bug.
`)
	fm, body, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "tdd-bugfix" {
		t.Errorf("Name: got %q, want tdd-bugfix", fm.Name)
	}
```
