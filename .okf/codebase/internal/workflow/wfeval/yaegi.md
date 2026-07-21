---
description: Source module internal/workflow/wfeval/yaegi.go (95 lines).
resource: internal/workflow/wfeval/yaegi.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: yaegi.go
type: Module
---

# Module yaegi.go

**Path**: `internal/workflow/wfeval/yaegi.go`  
**Lines**: 95

## Snippet Preview

```
// Package wfeval discovers and interprets a repo's custom workflow
// stages: .archie/stages/*.go files, each exporting a Stage() function
// that returns a workflow.Stage. Split from internal/workflow (and its
// generated symbol table in wfextract) to avoid the import cycle that
// would result from workflow depending on its own extracted symbols.
package wfeval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/wfextract"
)

// stagesDir is where a repo's custom stage scripts live, relative to
// the worktree root.
const stagesDir = ".archie/stages"

// Discover loads every .archie/stages/*.go file in dir and returns the
// workflow.Stage each exports, in filename order. A missing directory is
// not an error — it returns (nil, nil), meaning no custom stages are
// configured for this repo.
func Discover(dir string) ([]workflow.Stage, error) {
	entries, err := os.ReadDir(filepath.Join(dir, stagesDir))
```
