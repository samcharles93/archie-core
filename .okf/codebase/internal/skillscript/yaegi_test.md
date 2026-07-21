---
description: Source module internal/skillscript/yaegi_test.go (92 lines).
resource: internal/skillscript/yaegi_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: yaegi_test.go
type: Module
---

# Module yaegi_test.go

**Path**: `internal/skillscript/yaegi_test.go`  
**Lines**: 92

## Snippet Preview

```
package skillscript

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScript(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "script.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunCapturesStdout(t *testing.T) {
	path := writeScript(t, `package main

import "fmt"

func main() {
	fmt.Println("hello from a skill script")
}
`)
	out, err := Run(path)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
```
