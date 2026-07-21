---
description: Source module internal/skill/plugin_test.go (209 lines).
resource: internal/skill/plugin_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: plugin_test.go
type: Module
---

# Module plugin_test.go

**Path**: `internal/skill/plugin_test.go`  
**Lines**: 209

## Snippet Preview

```
package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPlugins(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "my-skill")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a SKILL.md so the skill directory is valid.
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: my-skill
description: A test skill
version: 1.0.0
---
Body.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write two plugin files.
	checkGo := `package main

```
