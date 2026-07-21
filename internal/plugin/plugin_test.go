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

// ── Adversarial tests ────────────────────────────────────────────────
//
// These tests try to BREAK LoadDir. A correct implementation must
// degrade gracefully — skip the bad plugin, load the rest, never panic.

func TestLoadDirNilFunctionFieldsLoadsWithoutPanic(t *testing.T) {
	// A _Plugin with nil WName/WVersion must LOAD without panicking
	// AND must not panic when Name()/Version() is called — the wrapper
	// must nil-guard. Otherwise logging plugin names at startup crashes
	// the daemon.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nilfuncs.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    nil,
	WVersion: nil,
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("LoadDir returned %d plugins, want 1 (nil funcs should not prevent loading)", len(plugins))
	}

	// These must not panic — the _Plugin wrapper must nil-guard.
	// The daemon calls Name()/Version() at startup to log loaded plugins.
	name := plugins[0].Name()
	version := plugins[0].Version()
	if name != "" {
		t.Errorf("Name() = %q, want empty string from nil WName", name)
	}
	if version != "" {
		t.Errorf("Version() = %q, want empty string from nil WVersion", version)
	}
}

func TestLoadDirPluginReturningEmptyNameVersion(t *testing.T) {
	// Plugins with empty name/version are still valid — the interface
	// doesn't forbid empty strings. LoadDir must not reject them.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "" },
	WVersion: func() string { return "" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("LoadDir returned %d plugins, want 1 (empty name is still valid)", len(plugins))
	}
}

func TestLoadDirSkipsPluginWithWrongExportType(t *testing.T) {
	// Plugin var exists but is an int, not a Plugin. Must be skipped.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wrongtype.go"), []byte(`package main

var Plugin = 42
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Errorf("LoadDir returned %d plugins from wrong-type export, want 0", len(plugins))
	}
}

func TestLoadDirSkipsPluginWithOnlySomeInterfaceMethods(t *testing.T) {
	// A struct that has Name() but not Version() — doesn't satisfy Plugin.
	// type assertion must fail and the plugin must be skipped.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partial.go"), []byte(`package main

type partial struct{}
func (p partial) Name() string { return "partial" }
// No Version() method — deliberately incomplete.

var Plugin partial
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Errorf("LoadDir returned %d plugins from partial impl, want 0", len(plugins))
	}
}

func TestLoadDirEmptyDirReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Errorf("LoadDir returned %d plugins from empty dir, want 0", len(plugins))
	}
	if plugins == nil {
		t.Error("LoadDir returned nil from empty dir, want empty slice")
	}
}

func TestLoadDirSubdirectoriesAreIgnored(t *testing.T) {
	// Only top-level .go files are loaded. Subdirectories are not traversed.
	dir := t.TempDir()
	subDir := filepath.Join(dir, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "nested" },
	WVersion: func() string { return "1.0.0" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Errorf("LoadDir returned %d plugins, want 0 — subdirectories must not be traversed", len(plugins))
	}
}

func TestLoadDirPluginWithPanicInNameFunc(t *testing.T) {
	// A plugin whose Name() function panics must still be loadable —
	// the panic only fires on call, not on load.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panicky.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { panic("boom") },
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
		t.Fatalf("LoadDir returned %d plugins, want 1 (panics are deferred to call time)", len(plugins))
	}

	// Verify calling Name() actually panics — the plugin loaded, the panic
	// is the caller's responsibility.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Name() did not panic — expected panic from panicky plugin")
			}
		}()
		plugins[0].Name()
	}()
}

func TestLoadDirPluginWithUnusedImport(t *testing.T) {
	// Yaegi should still eval a file that imports a package but doesn't
	// use it directly (the import is needed for the _Plugin type).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unusedimp.go"), []byte(`package main

import "fmt"

var Plugin = struct{}{}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// This must not crash — just skip.
	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal("LoadDir errored on unused import:", err)
	}
	_ = plugins
}

func TestLoadDirPluginWithGoBuildTag(t *testing.T) {
	// Build tags in Yaegi-interpreted code are just comments — they
	// don't gate evaluation. The file loads normally.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tagged.go"), []byte(`//go:build ignore

package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "tagged" },
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
		t.Errorf("LoadDir returned %d plugins from tagged file, want 1 (build tags are ignored in Yaegi)", len(plugins))
	}
}
