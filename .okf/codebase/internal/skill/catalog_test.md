---
description: Source module internal/skill/catalog_test.go (211 lines).
resource: internal/skill/catalog_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: catalog_test.go
type: Module
---

# Module catalog_test.go

**Path**: `internal/skill/catalog_test.go`  
**Lines**: 211

## Snippet Preview

```
package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── progressive disclosure: catalog tier ─────────────────────────────

func TestCatalogReturnsNameAndDescriptionOnly(t *testing.T) {
	// Catalog tier (~100 tokens per skill): name + description loaded at
	// daemon startup. Full body is Tier 2, loaded on skill activation.
	// Currently Discover() returns the full Skill (frontmatter + body).
	// Catalog() must return only the catalog entries without bodies.

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-tdd")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: archie-wf-tdd
description: TDD bugfix workflow — reproduce, prove, fix.
version: 1.0.0
metadata:
  archie:
    tools: [go]
    engine: any
```
