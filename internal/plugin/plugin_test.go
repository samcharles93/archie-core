package plugin_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// ── Plugin interface ─────────────────────────────────────────────────

func TestPluginInterfaceExists(t *testing.T) {
	var p plugin.Plugin
	_ = p
	type nameVersioner interface {
		Name() string
		Version() string
	}
	var _ nameVersioner = p
}

func TestPartialImplementationIsRejectedBeforeWrapping(t *testing.T) {
	// Why the generated interface wrappers carry no nil-guards: Yaegi
	// type-checks the assignment to the interface before it ever builds a
	// wrapper, so a type missing a method never reaches Go as a wrapper with
	// a nil method field. Guarding the generated wrappers by hand defends a
	// state Yaegi cannot produce, and every regeneration silently drops it.
	i, err := yaegiutil.New(interp.Options{}, symbols)
	if err != nil {
		t.Fatal(err)
	}
	_, err = yaegiutil.Resolve[plugin.Plugin](i, `package main

import "github.com/samcharles93/archie-core/internal/plugin"

type partial struct{}

func (partial) Name() string { return "partial" }

var Plugin plugin.Plugin = partial{}
`, "main.Plugin")
	if err == nil {
		t.Fatal("want error for a Plugin missing Version(), got nil")
	}
	if !strings.Contains(err.Error(), "cannot use type") {
		t.Errorf("want a Yaegi assignment type error, got: %v", err)
	}
}

func TestCompleteImplementationWrapsWithNoNilMethods(t *testing.T) {
	i, err := yaegiutil.New(interp.Options{}, symbols)
	if err != nil {
		t.Fatal(err)
	}
	p, err := yaegiutil.Resolve[plugin.Plugin](i, `package main

import "github.com/samcharles93/archie-core/internal/plugin"

type complete struct{}

func (complete) Name() string    { return "n" }
func (complete) Version() string { return "v" }

var Plugin plugin.Plugin = complete{}
`, "main.Plugin")
	if err != nil {
		t.Fatal(err)
	}
	rv := reflect.ValueOf(p)
	if rv.Kind() != reflect.Struct {
		t.Fatalf("want a Yaegi wrapper struct, got %T", p)
	}
	for j := range rv.NumField() {
		f := rv.Type().Field(j)
		if f.Type.Kind() == reflect.Func && rv.Field(j).IsNil() {
			t.Errorf("wrapper field %s is nil; Yaegi is expected to populate every method", f.Name)
		}
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
	// A .go file that doesn't compile must be skipped  --  not crash the loader.
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
// degrade gracefully  --  skip the bad plugin, load the rest, never panic.

func TestLoadDirRefusesWrapperWithNilFunctionFields(t *testing.T) {
	// A _Plugin with nil WName/WVersion must be refused at load. It
	// previously loaded and answered "" for both, so the daemon logged a
	// nameless plugin as if it were healthy.
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
	if len(plugins) != 0 {
		t.Fatalf("LoadDir returned %d plugins, want 0 (a wrapper missing methods must be refused)", len(plugins))
	}
}

func TestLoadDirPluginReturningEmptyNameVersion(t *testing.T) {
	// Plugins with empty name/version are still valid  --  the interface
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
	// A struct that has Name() but not Version()  --  doesn't satisfy Plugin.
	// type assertion must fail and the plugin must be skipped.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partial.go"), []byte(`package main

type partial struct{}
func (p partial) Name() string { return "partial" }
// No Version() method  --  deliberately incomplete.

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

func TestLoadDirSkipsHandBuiltPartialWrapper(t *testing.T) {
	// Yaegi type-checks "var Plugin plugin.Plugin = impl{}" and refuses an
	// incomplete impl, but interpreted code can also build the generated
	// wrapper directly, where struct-literal semantics leave an omitted method
	// nil and the call panics at first use. LoadDir must refuse it instead.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partialwrap.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName: func() string { return "halfbuilt" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("LoadDir returned %d plugins from a wrapper missing WVersion, want 0", len(plugins))
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
		t.Errorf("LoadDir returned %d plugins, want 0  --  subdirectories must not be traversed", len(plugins))
	}
}

func TestLoadDirPluginWithPanicInNameFunc(t *testing.T) {
	// A plugin whose Name() function panics must still be loadable  --
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

	// Verify calling Name() actually panics  --  the plugin loaded, the panic
	// is the caller's responsibility.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Name() did not panic  --  expected panic from panicky plugin")
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

	// This must not crash  --  just skip.
	plugins, err := plugin.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal("LoadDir errored on unused import:", err)
	}
	_ = plugins
}

// The former TestGeneratedWrapperHasNilGuards was retired. It pinned
// hand-added nil guards in the generated wrapper, which every regeneration
// silently dropped. LoadDir now refuses such a plugin outright; see
// TestLoadDirRefusesWrapperWithNilFunctionFields.

func TestLoadDirPluginWithGoBuildTag(t *testing.T) {
	// Build tags in Yaegi-interpreted code are just comments  --  they
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

// ── Timeout tests (issue #43) ────────────────────────────────────────
//
// LoadDir calls yaegiutil.Resolve which calls i.Eval(src) with no
// context, deadline, or goroutine+select guard. A plugin whose
// package-level init code never returns (e.g. select{} in init())
// hangs the entire daemon startup forever. These tests prove the bug:
// they assert the CORRECT behaviour (timeout → skip the bad plugin)
// and FAIL today because LoadDir hangs before returning.

func TestLoadDirTimeoutOnBlockingPlugin(t *testing.T) {
	// A single plugin whose init() blocks forever must be skipped,
	// not hang LoadDir indefinitely. The daemon must start with zero
	// plugins and no error — the blocking file is logged and skipped.

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blocking.go"), []byte(`package main

func init() { select {} }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var plugins []plugin.Plugin
	var loadErr error
	go func() {
		plugins, loadErr = plugin.LoadDir(dir, symbols)
		close(done)
	}()

	select {
	case <-done:
		if loadErr != nil {
			t.Errorf("LoadDir returned error: %v", loadErr)
		}
		if len(plugins) != 0 {
			t.Errorf("LoadDir returned %d plugins, want 0 (blocking plugin should be skipped)", len(plugins))
		}
	case <-time.After(5 * time.Second):
		t.Error("LoadDir did not return within 5s — plugin eval has no timeout (issue #43)")
	}
}

func TestLoadDirSkipsBlockingPluginButLoadsGoodOnes(t *testing.T) {
	// A blocking plugin must not prevent good plugins from loading.
	// LoadDir must return within the timeout with only the valid plugin.

	dir := t.TempDir()
	// Blocking plugin first (sorted first by filename).
	if err := os.WriteFile(filepath.Join(dir, "00-blocking.go"), []byte(`package main

func init() { select {} }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Good plugin second.
	if err := os.WriteFile(filepath.Join(dir, "01-good.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "good" },
	WVersion: func() string { return "1.0.0" },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var plugins []plugin.Plugin
	var loadErr error
	go func() {
		plugins, loadErr = plugin.LoadDir(dir, symbols)
		close(done)
	}()

	select {
	case <-done:
		if loadErr != nil {
			t.Errorf("LoadDir returned error: %v", loadErr)
		}
		if len(plugins) != 1 {
			t.Errorf("LoadDir returned %d plugins, want 1 (blocking plugin should be skipped, good plugin loaded)", len(plugins))
		} else if plugins[0].Name() != "good" {
			t.Errorf("plugin.Name() = %q, want good", plugins[0].Name())
		}
	case <-time.After(5 * time.Second):
		t.Error("LoadDir did not return within 5s — blocking plugin hang prevents loading good plugins (issue #43)")
	}
}
