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
	}
	if len(fm.Metadata.Archie.Plugins) != 2 {
		t.Error("Gap 8: Frontmatter.Metadata.Archie has no Plugins field, or " +
			"it didn't parse the plugins list. Add a Plugins []string field to " +
			"the Metadata.Archie struct per PRD section 5.")
	}
	if fm.Metadata.Archie.Plugins[0] != "plugins/custom-gate.go" {
		t.Errorf("Plugins[0] = %q, want plugins/custom-gate.go", fm.Metadata.Archie.Plugins[0])
	}
}
