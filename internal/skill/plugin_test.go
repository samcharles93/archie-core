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

func TestLoadPluginsWithAllowlist(t *testing.T) {
	// When allowed is non-empty, only those plugins are loaded in declared order.
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "ordered")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write three plugin files.
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		src := `package main

func Run(input string) string {
	return "` + name + `"
}
`
		if err := os.WriteFile(filepath.Join(pluginsDir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Allowlist only b.go and a.go (with plugins/ prefix), in that order.
	plugins, err := LoadPlugins(dir, "ordered", []string{"plugins/b.go", "plugins/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(plugins))
	}
	if plugins[0].Name != "b" {
		t.Errorf("plugin[0].Name = %q, want b", plugins[0].Name)
	}
	if plugins[1].Name != "a" {
		t.Errorf("plugin[1].Name = %q, want a", plugins[1].Name)
	}
}

func TestLoadPluginsWithAllowlistMissingFile(t *testing.T) {
	// When allowed references a file not on disk, return an error.
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "broken")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "a.go"), []byte(`package main

func Run(input string) string { return "ok" }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPlugins(dir, "broken", []string{"a.go", "nonexistent.go"})
	if err == nil {
		t.Fatal("expected error for missing plugin file, got nil")
	}
}

func TestLoadPluginsEmptyAllowlist(t *testing.T) {
	// Nil or empty allowlist falls back to glob-all (same as DiscoverPlugins).
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "glob")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.go", "a.go"} {
		if err := os.WriteFile(filepath.Join(pluginsDir, name), []byte(`package main

func Run(input string) string { return "x" }
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// nil allowed → glob all, sorted alphabetically.
	plugins, err := LoadPlugins(dir, "glob", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(plugins))
	}
	if plugins[0].Name != "a" {
		t.Errorf("plugin[0].Name = %q, want a (alphabetical)", plugins[0].Name)
	}
	if plugins[1].Name != "b" {
		t.Errorf("plugin[1].Name = %q, want b (alphabetical)", plugins[1].Name)
	}

	// Empty allowed → same as nil.
	plugins2, err := LoadPlugins(dir, "glob", []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins2) != 2 {
		t.Fatalf("got %d plugins (empty allowlist), want 2", len(plugins2))
	}
}
