---
description: Source module internal/plugin/plugin.go (96 lines).
resource: internal/plugin/plugin.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: plugin.go
type: Module
---

# Module plugin.go

**Path**: `internal/plugin/plugin.go`  
**Lines**: 96

## Snippet Preview

```
// Package plugin defines the core plugin interface for extending archie-core's
// daemon at startup. Plugins are Yaegi-interpreted .go files loaded from
// ~/.config/archie/plugins/ that register themselves with the daemon's
// extension registry. PRD section 5 Layer 2.
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// Plugin is a core plugin loaded by the daemon at startup. Each .go file
// in ~/.config/archie/plugins/ must export a variable named "Plugin" that
// satisfies this interface.
type Plugin interface {
	Name() string
	Version() string
}

// Registry holds loaded plugins and dispatches extension point calls.
type Registry struct {
	plugins []Plugin
}
```
