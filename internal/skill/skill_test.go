package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	src := []byte(`---
name: tdd-bugfix
description: Fix bugs with TDD
version: 1.0.0
metadata:
  archie:
    tools: [go, golangci-lint]
    engine: any
---

## Stage 1: Analyse

Read the code and find the bug.
`)
	fm, body, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "tdd-bugfix" {
		t.Errorf("Name: got %q, want tdd-bugfix", fm.Name)
	}
	if fm.Version != "1.0.0" {
		t.Errorf("Version: got %q, want 1.0.0", fm.Version)
	}
	if fm.Metadata.Archie == nil {
		t.Fatal("Metadata.Archie is nil")
	}
	if len(fm.Metadata.Archie.Tools) != 2 || fm.Metadata.Archie.Tools[0] != "go" {
		t.Errorf("Tools: got %v", fm.Metadata.Archie.Tools)
	}
	if fm.Metadata.Archie.Engine != "any" {
		t.Errorf("Engine: got %q, want any", fm.Metadata.Archie.Engine)
	}
	if body != "## Stage 1: Analyse\n\nRead the code and find the bug." {
		t.Errorf("Body: got %q", body)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	src := []byte(`# Just markdown

No YAML frontmatter here.
`)
	fm, body, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "" {
		t.Errorf("Name should be empty, got %q", fm.Name)
	}
	if body != string(src) {
		t.Errorf("Body should be the full content")
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills")

	// Create tdd-bugfix skill.
	tddDir := filepath.Join(skillsDir, "tdd-bugfix")
	if err := os.MkdirAll(tddDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tddDir, "SKILL.md"), []byte(`---
name: tdd-bugfix
description: TDD bugfix workflow
version: 1.0.0
metadata:
  archie:
    tools: [go]
    engine: any
---

Body content here.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create ecosystem-node skill.
	nodeDir := filepath.Join(skillsDir, "ecosystem-node")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "SKILL.md"), []byte(`---
name: ecosystem-node
description: Node conventions
version: 1.0.0
metadata:
  archie:
    tools: [node, npm]
    engine: any
---

Node stuff.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(skills))
	}
	if _, ok := skills["tdd-bugfix"]; !ok {
		t.Error("missing tdd-bugfix")
	}
	if _, ok := skills["ecosystem-node"]; !ok {
		t.Error("missing ecosystem-node")
	}
	if skills["tdd-bugfix"].Body != "Body content here." {
		t.Errorf("tdd body: got %q", skills["tdd-bugfix"].Body)
	}
}

func TestDiscoverMissing(t *testing.T) {
	dir := t.TempDir()
	skills, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if skills != nil {
		t.Errorf("expected nil map for missing dir, got %v", skills)
	}
}

func TestLoadBody(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "tdd-bugfix")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: tdd-bugfix
description: desc
version: 1.0.0
---
Body content.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	body := LoadBody(dir, "tdd-bugfix")
	if body != "Body content." {
		t.Errorf("got %q, want 'Body content.'", body)
	}

	// Non-existent skill returns empty.
	body = LoadBody(dir, "nonexistent")
	if body != "" {
		t.Errorf("expected empty, got %q", body)
	}
}
