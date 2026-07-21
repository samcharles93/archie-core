---
description: Source module internal/workflow/skillbuild/skillbuild_test.go (741 lines).
resource: internal/workflow/skillbuild/skillbuild_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: skillbuild_test.go
type: Module
---

# Module skillbuild_test.go

**Path**: `internal/workflow/skillbuild/skillbuild_test.go`  
**Lines**: 741

## Snippet Preview

```
package skillbuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/workflow"
)

// ── workflows built from skill plugins ────────────────────────────────

func TestBuildWorkflowFromSkillPlugins(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "archie-wf-greet")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-greet
description: A custom greeting workflow built entirely from plugins.
version: 1.0.0
metadata:
  archie:
    workflow: greet
---
Greet the world, then say goodbye.
```
