---
description: Source module internal/workflow/wfeval/yaegi_test.go (139 lines).
resource: internal/workflow/wfeval/yaegi_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: yaegi_test.go
type: Module
---

# Module yaegi_test.go

**Path**: `internal/workflow/wfeval/yaegi_test.go`  
**Lines**: 139

## Snippet Preview

```
package wfeval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func writeStageScript(t *testing.T, dir, name, src string) {
	t.Helper()
	stagesDir := filepath.Join(dir, ".archie", "stages")
	if err := os.MkdirAll(stagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagesDir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverNoStagesDir(t *testing.T) {
	stages, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil (no .archie/stages)", err)
	}
	if stages != nil {
		t.Fatalf("Discover() = %v, want nil", stages)
	}
```
