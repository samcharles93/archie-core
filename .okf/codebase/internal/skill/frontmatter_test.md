---
description: Source module internal/skill/frontmatter_test.go (40 lines).
resource: internal/skill/frontmatter_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: frontmatter_test.go
type: Module
---

# Module frontmatter_test.go

**Path**: `internal/skill/frontmatter_test.go`  
**Lines**: 40

## Snippet Preview

```
package skill

import "testing"

// ── regression: Gap 8 — frontmatter missing plugins key ─────────────

func TestFrontmatterMetadataArchieHasPluginsField(t *testing.T) {
	// Gap 8: Frontmatter.Metadata.Archie has Tools and Engine only.
	// PRD section 5 shows a plugins array in metadata.archie:
	//   plugins: [plugins/custom-gate.go, plugins/gosec-check.go]

	fm, _, err := Parse([]byte(`---
name: test-skill
description: desc
version: 1.0.0
metadata:
  archie:
    tools: [go]
    engine: any
    plugins:
      - plugins/custom-gate.go
      - plugins/gosec-check.go
---
Body.
`))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Metadata.Archie == nil {
		t.Fatal("Metadata.Archie is nil")
```
