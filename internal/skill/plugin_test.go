package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPlugins(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "my-skill")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a SKILL.md so the skill directory is valid.
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: my-skill
description: A test skill
version: 1.0.0
---
Body.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write two plugin files.
	checkGo := `package main

import "fmt"

func Run(input string) string {
	return "checked: " + input
}
`
	if err := os.WriteFile(filepath.Join(pluginsDir, "check.go"), []byte(checkGo), 0o644); err != nil {
		t.Fatal(err)
	}

	lintGo := `package main

import "strings"

func Run(input string) string {
	if strings.Contains(input, "TODO") {
		return "found TODO"
	}
	return "clean"
}
`
	if err := os.WriteFile(filepath.Join(pluginsDir, "lint.go"), []byte(lintGo), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := DiscoverPlugins(dir, "my-skill")
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(plugins))
	}

	// Plugins are returned sorted by name.
	if plugins[0].Name != "check" {
		t.Errorf("plugin[0].Name = %q, want check", plugins[0].Name)
	}
	if plugins[1].Name != "lint" {
		t.Errorf("plugin[1].Name = %q, want lint", plugins[1].Name)
	}

	// Verify source content is loaded.
	if plugins[0].Src != checkGo {
		t.Errorf("check plugin source mismatch")
	}
	if plugins[1].Src != lintGo {
		t.Errorf("lint plugin source mismatch")
	}
}

func TestDiscoverPluginsNone(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "no-plugins")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: no-plugins
description: desc
version: 1.0.0
---
Body.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := DiscoverPlugins(dir, "no-plugins")
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("got %d plugins, want 0", len(plugins))
	}
}

func TestDiscoverPluginsMissingSkill(t *testing.T) {
	dir := t.TempDir()
	plugins, err := DiscoverPlugins(dir, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if plugins != nil {
		t.Fatalf("got %v, want nil for missing skill", plugins)
	}
}

func TestPluginRun(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "runner")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: runner
description: desc
version: 1.0.0
---
Body.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	src := `package main

func Run(input string) string {
	return "result: " + input
}
`
	if err := os.WriteFile(filepath.Join(pluginsDir, "echo.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := DiscoverPlugins(dir, "runner")
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(plugins))
	}

	// Run the plugin via Yaegi and verify the output.
	out, err := plugins[0].Run("hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "result: hello" {
		t.Errorf("Run() = %q, want %q", out, "result: hello")
	}
}

func TestDiscoverIncludesPlugins(t *testing.T) {
	// Verify that Discover() populates the Plugins field on Skill.
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "full-skill")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: full-skill
description: Has plugins
version: 1.0.0
metadata:
  archie:
    tools: [go]
    engine: any
---
Skill body here.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "gate.go"), []byte(`package main

func Run(input string) string {
	return "gated"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	s := skills["full-skill"]
	if s.Body != "Skill body here." {
		t.Errorf("Body = %q", s.Body)
	}
	if len(s.Plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(s.Plugins))
	}
	if s.Plugins[0].Name != "gate" {
		t.Errorf("plugin name = %q, want gate", s.Plugins[0].Name)
	}
}
