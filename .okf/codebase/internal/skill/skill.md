---
description: Source module internal/skill/skill.go (193 lines).
resource: internal/skill/skill.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: skill.go
type: Module
---

# Module skill.go

**Path**: `internal/skill/skill.go`  
**Lines**: 193

## Snippet Preview

```
// Package skill parses agentskills.io SKILL.md files and discovers skills
// from the .agents/skills/ directory convention. It follows the same discovery
// pattern as internal/workflow/wfeval (missing directory is not an error).
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the parsed YAML frontmatter of a SKILL.md file.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
	Metadata    struct {
		Archie *struct {
			Tools    []string `yaml:"tools"`
			Engine   string   `yaml:"engine"`
			Plugins  []string `yaml:"plugins,omitempty"`
			Workflow string   `yaml:"workflow,omitempty"`
		} `yaml:"archie"`
	} `yaml:"metadata"`
}

```
