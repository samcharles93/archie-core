---
description: Source module internal/workflow/plugin_test.go (106 lines).
resource: internal/workflow/plugin_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: plugin_test.go
type: Module
---

# Module plugin_test.go

**Path**: `internal/workflow/plugin_test.go`  
**Lines**: 106

## Snippet Preview

```
package workflow

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
)

// ── regression: Gap 7 — plugins discovered but not executed ─────────

func TestSkillPluginsAvailableDuringStageExecution(t *testing.T) {
	// Gap 7: skill.Discover() populates Skill.Plugins but nothing calls
	// plugin.Run() during stage execution. Plugins must be loaded from
	// the skill directory alongside the SKILL.md body and made available
	// to the agent as tools during stage runs.
	//
	// This test creates a worktree with a skill that has a bundled plugin.
	// It runs an AgentStage and verifies the plugin was loaded and is
	// accessible via the TaskContext.

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-tdd")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
```
