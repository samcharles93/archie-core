---
description: Source module internal/skill/plugin.go (83 lines).
resource: internal/skill/plugin.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: plugin.go
type: Module
---

# Module plugin.go

**Path**: `internal/skill/plugin.go`  
**Lines**: 83

## Snippet Preview

```
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// Plugin is a discovered Yaegi-interpreted .go file in a skill's plugins/
// directory. The source must be a valid Go package exporting a Run function:
//
//	func Run(input string) string
type Plugin struct {
	Name string // filename without .go extension
	Path string // full path to the .go file
	Src  string // source code content
}

// Run interprets the plugin source via Yaegi and calls its Run function
// with the given input. The plugin runs with full standard library access.
func (p *Plugin) Run(input string) (string, error) {
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		return "", fmt.Errorf("plugin %s: stdlib: %w", p.Name, err)
	}
```
