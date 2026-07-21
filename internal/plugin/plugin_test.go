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

// ── LoadDir behavioral tests ─────────────────────────────────────────

var symbols = pluginextract.Symbols

func TestLoadDirLoadsValidPlugin(t *testing.T) {
	// LoadDir must Yaegi-interpret .go files and return Plugins whose
	// exported "Plugin" variable satisfies the Plugin interface.

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "1.0.0" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("LoadDir returned %d plugins, want 1", len(plugins))
	}
	if plugins[0].Name() != "hello" {
		t.Errorf("plugin.Name() = %q, want hello", plugins[0].Name())
	}
	if plugins[0].Version() != "1.0.0" {
		t.Errorf("plugin.Version() = %q, want 1.0.0", plugins[0].Version())
	}
}

func TestLoadDirSkipsInvalidPlugin(t *testing.T) {
	// A .go file that doesn't compile must be skipped — not crash the loader.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte(`package main
this is not valid Go syntax @@@@
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Errorf("LoadDir returned %d plugins from broken source, want 0", len(plugins))
	}
}

func TestLoadDirSkipsFileWithoutPluginExport(t *testing.T) {
	// A valid Go file without a "Plugin" export must be skipped.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notaplugin.go"), []byte(`package main

func Something() string { return "nope" }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Errorf("LoadDir returned %d plugins from file without Plugin export, want 0", len(plugins))
	}
}

func TestLoadDirSkipsNonGoFiles(t *testing.T) {
	// Only .go files are loaded. Other files are ignored.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte(`# Not a plugin`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "1.0.0" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("LoadDir returned %d plugins, want 1 (only .go files count)", len(plugins))
	}
}

func TestLoadDirMultiplePlugins(t *testing.T) {
	// Multiple valid plugins must all be loaded, sorted by filename.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01-alpha.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "alpha" },
	WVersion: func() string { return "1.0.0" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "02-beta.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "beta" },
	WVersion: func() string { return "2.0.0" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("LoadDir returned %d plugins, want 2", len(plugins))
	}
	if plugins[0].Name() != "alpha" {
		t.Errorf("plugin[0].Name() = %q, want alpha (sorted by filename)", plugins[0].Name())
	}
	if plugins[1].Name() != "beta" {
		t.Errorf("plugin[1].Name() = %q, want beta", plugins[1].Name())
	}
}

func TestLoadDirNonexistentDirReturnsNil(t *testing.T) {
	plugins, err := plugin.LoadDir("/nonexistent/path/for/plugins")
	if err != nil {
		t.Fatal(err)
	}
	if plugins != nil {
		t.Errorf("LoadDir returned %v, want nil for nonexistent directory", plugins)
	}
}
