---
description: Source module internal/workflow/skillbuild/skillbuild.go (184 lines).
resource: internal/workflow/skillbuild/skillbuild.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: skillbuild.go
type: Module
---

# Module skillbuild.go

**Path**: `internal/workflow/skillbuild/skillbuild.go`  
**Lines**: 184

## Snippet Preview

```
// Package skillbuild constructs Workflows from skill catalog entries by
// loading Yaegi-interpreted stage plugins from the skill's plugins/ directory.
package skillbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/wfextract"
)

// builtins returns the hardcoded fallback workflows. These are used when
// no skill declares a given workflow name in its metadata.archie.workflow.
func builtins() workflow.Registry {
	return workflow.Registry{
		"bootstrap":   workflow.Bootstrap(),
		"implement":   workflow.Implement(),
		"tdd":         workflow.TDD(),
		"feasibility": workflow.Feasibility(),
		"default":     workflow.Implement(),
	}
```
