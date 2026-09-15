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
	// Catalog() must return only the catalog entries without bodies.

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-tdd")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: archie-wf-tdd
description: TDD bugfix workflow  --  reproduce, prove, fix.
version: 1.0.0
metadata:
  archie:
    tools: [go]
    engine: any
---
This is the full SKILL.md body with detailed stage instructions.
It should NOT appear in the catalog entry.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d catalog entries, want 1", len(entries))
	}

	e := entries[0]
	if e.Name != "archie-wf-tdd" {
		t.Errorf("Name = %q, want archie-wf-tdd", e.Name)
	}
	if e.Description == "" {
		t.Error("Description is empty  --  catalog must include the description")
	}
	if strings.Contains(e.Description, "full SKILL.md body") {
		t.Error("Catalog entry contains the body text. " +
			"Catalog must only contain name + description (~100 tokens). " +
			"The full body is Tier 2, loaded on skill activation.")
	}

	// The full body must still be available via LoadBody.
	body := LoadBody(dir, "archie-wf-tdd")
	if !strings.Contains(body, "full SKILL.md body") {
		t.Error("LoadBody did not return the full body. " +
			"Catalog and LoadBody must be separate tiers: " +
			"Catalog = name+description (startup), LoadBody = full SKILL.md (activation).")
	}
}

func TestCatalogMissingDirReturnsEmpty(t *testing.T) {
	entries, err := Catalog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Errorf("expected nil for missing dir, got %v", entries)
	}
}

func TestCatalogRootsUsesPrecedenceAndTracksRoot(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	writeSkill := func(root, name, description string) {
		dir := filepath.Join(root, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + description + "\n---\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeSkill(project, "shared-name", "project copy")
	writeSkill(project, "project-only", "project skill")
	writeSkill(global, "shared-name", "global copy")
	writeSkill(global, "global-only", "global skill")

	entries, err := CatalogRoots(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d catalog entries, want 3", len(entries))
	}
	for _, entry := range entries {
		if entry.Name == "shared-name" {
			if entry.Description != "project copy" || entry.Root != project {
				t.Fatalf("shared-name = %#v, want project entry", entry)
			}
		}
	}
}

// ── archie-wf-* naming convention ────────────────────────────────────

func TestSkillsUseArchiewfNaming(t *testing.T) {
	// PRD section 5: skills follow archie-wf-* naming convention.
	// The directory name must match the workflow: tdd→archie-wf-tdd,
	// implement→archie-wf-implement, feasibility→archie-wf-feasibility.
	//
	// Currently skill directories are named tdd-bugfix, ecosystem-node, etc.

	dir := t.TempDir()
	skills := map[string]string{
		"archie-wf-tdd":         "TDD bugfix workflow",
		"archie-wf-implement":   "Implement workflow",
		"archie-wf-feasibility": "Feasibility workflow",
	}

	for name, desc := range skills {
		skillDir := filepath.Join(dir, ".agents", "skills", name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `---
name: ` + name + `
description: ` + desc + `
version: 1.0.0
---
Body.
`
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d catalog entries, want 3", len(entries))
	}

	for _, e := range entries {
		if !strings.HasPrefix(e.Name, "archie-wf-") {
			t.Errorf("skill %q does not follow archie-wf-* naming convention. "+
				"Per PRD section 5, all workflow skills must use the archie-wf-* prefix.", e.Name)
		}
	}
}

// ── CatalogEntry type ────────────────────────────────────────────────

func TestCatalogEntryHasRequiredFields(t *testing.T) {
	// CatalogEntry must have Name, Description, and a way to load the
	// full body on activation.
	e := CatalogEntry{Name: "archie-wf-tdd", Description: "TDD workflow"}
	if e.Name == "" || e.Description == "" {
		t.Error("CatalogEntry must have non-empty Name and Description")
	}
}
