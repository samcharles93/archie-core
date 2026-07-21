---
description: Source module internal/gate/gateeval/yaegi_test.go (145 lines).
resource: internal/gate/gateeval/yaegi_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: yaegi_test.go
type: Module
---

# Module yaegi_test.go

**Path**: `internal/gate/gateeval/yaegi_test.go`  
**Lines**: 145

## Snippet Preview

```
package gateeval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/gate"
)

func writeGateScript(t *testing.T, dir, src string) {
	t.Helper()
	archieDir := filepath.Join(dir, ".archie")
	if err := os.MkdirAll(archieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archieDir, "gate.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateNoScript(t *testing.T) {
	dir := t.TempDir()
	findings, err := Evaluate(gate.GateContext{Dir: dir})
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil (no script configured)", err)
	}
	if findings != nil {
		t.Fatalf("Evaluate() findings = %v, want nil", findings)
	}
```
