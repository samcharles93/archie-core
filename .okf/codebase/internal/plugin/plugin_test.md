---
description: Source module internal/plugin/plugin_test.go (417 lines).
resource: internal/plugin/plugin_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: plugin_test.go
type: Module
---

# Module plugin_test.go

**Path**: `internal/plugin/plugin_test.go`  
**Lines**: 417

## Snippet Preview

```
package plugin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
)

// ── Plugin interface and registry ────────────────────────────────────

func TestPluginInterfaceExists(t *testing.T) {
	var p plugin.Plugin
	_ = p
	type nameVersioner interface {
		Name() string
		Version() string
	}
	var _ nameVersioner = p
}

func TestRegistryExists(t *testing.T) {
	r := &plugin.Registry{}
	if r == nil {
		t.Fatal("Registry type is not defined")
	}
}

```
