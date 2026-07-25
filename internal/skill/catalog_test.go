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
	// Currently Discover() returns the full Skill (frontmatter + body).
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

// ── bundled plugin demo skill ────────────────────────────────────────

func TestBundledPluginSkillIsDiscoverableAndRunnable(t *testing.T) {
	// A skill must be able to ship bundled Yaegi plugins in its plugins/
	// directory. When metadata.archie.plugins is present, only the listed
	// plugins are loaded in declared order; unlisted *.go files are ignored.

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-tdd")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: archie-wf-tdd
description: TDD bugfix workflow with security checks
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint]
    engine: any
    plugins:
      - plugins/security-check.go
      - plugins/lint-check.go
---
Full body content.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write the listed plugins.
	if err := os.WriteFile(filepath.Join(pluginsDir, "security-check.go"), []byte(`package main

import "strings"

func Run(input string) string {
	if strings.Contains(input, "TODO") {
		return "found TODO"
	}
	return "clean"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "lint-check.go"), []byte(`package main

func Run(input string) string {
	return "linted: " + input
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write an unlisted plugin  --  it must NOT be loaded.
	if err := os.WriteFile(filepath.Join(pluginsDir, "extra.go"), []byte(`package main

func Run(input string) string {
	return "extra"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Discover the skill  --  must include only the listed plugins in declared order.
	skills, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	s := skills["archie-wf-tdd"]
	if len(s.Plugins) != 2 {
		t.Fatalf("skill has %d plugins, want 2  --  Discover() must honor metadata.archie.plugins", len(s.Plugins))
	}
	if s.Plugins[0].Name != "security-check" {
		t.Errorf("plugin[0].Name = %q, want security-check (declared order)", s.Plugins[0].Name)
	}
	if s.Plugins[1].Name != "lint-check" {
		t.Errorf("plugin[1].Name = %q, want lint-check (declared order)", s.Plugins[1].Name)
	}

	// The plugin must be runnable.
	out, err := s.Plugins[0].Run("code with TODO")
	if err != nil {
		t.Fatal(err)
	}
	if out != "found TODO" {
		t.Errorf("plugin output = %q, want 'found TODO'", out)
	}

	// The frontmatter must declare the plugin.
	if s.Frontmatter.Metadata.Archie == nil || len(s.Frontmatter.Metadata.Archie.Plugins) != 2 {
		t.Error("Frontmatter.Metadata.Archie.Plugins is empty  --  " +
			"the SKILL.md must declare bundled plugins in the metadata.archie.plugins list")
	}
}
