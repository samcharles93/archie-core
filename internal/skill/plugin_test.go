package skill

import (
	"os"
	"path/filepath"
	"testing"
)

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

	plugins, err := LoadPlugins(dir, "runner", nil)
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
	// Nil or empty allowlist falls back to glob-all.
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
