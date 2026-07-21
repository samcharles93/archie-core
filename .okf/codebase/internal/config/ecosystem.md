---
description: Source module internal/config/ecosystem.go (30 lines).
resource: internal/config/ecosystem.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: ecosystem.go
type: Module
---

# Module ecosystem.go

**Path**: `internal/config/ecosystem.go`  
**Lines**: 30

## Snippet Preview

```
package config

// ecosystemDefaults holds the per-ecosystem default preflight check and
// test-file glob. An ecosystem not listed here (or "custom") means: no
// preflight at all, empty test glob (protection no-ops).
type ecosystemDefaults struct {
	Preflight [][]string
	TestGlob  string
}

// ecosystems maps known ecosystem names to their defaults.
// "custom" is deliberately absent — it means user-configured everything.
var ecosystems = map[string]ecosystemDefaults{
	"go": {
		Preflight: [][]string{{"go", "version"}},
		TestGlob:  "*_test.go",
	},
	"python": {
		Preflight: [][]string{{"python", "--version"}},
		TestGlob:  "test_*.py",
	},
	"node": {
		Preflight: [][]string{{"node", "--version"}},
		TestGlob:  "*.test.ts",
	},
	"rust": {
		Preflight: [][]string{{"cargo", "--version"}},
		TestGlob:  "tests/*.rs",
	},
}
```
