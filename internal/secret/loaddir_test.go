package secret_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/secret/secretextract"
)

// ── LoadDir behavioral tests ─────────────────────────────────────────

var symbols = secretextract.Symbols

func TestLoadDirLoadsValidEngine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "1.0.0" },
	WResolve: func(key string) (string, error) { return "resolved:" + key, nil },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("LoadDir returned %d, want 1", n)
	}
	e, ok := r.Get("hello")
	if !ok {
		t.Fatal("engine \"hello\" not registered")
	}
	if e.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want 1.0.0", e.Version())
	}
	v, err := e.Resolve("k")
	if err != nil {
		t.Fatal(err)
	}
	if v != "resolved:k" {
		t.Errorf("Resolve() = %q, want resolved:k", v)
	}
}

func TestLoadDirSkipsInvalidEngine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte(`package main
this is not valid Go syntax @@@@
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("LoadDir returned %d from broken source, want 0", n)
	}
}

func TestLoadDirSkipsFileWithoutEngineExport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notanengine.go"), []byte(`package main

func Something() string { return "nope" }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("LoadDir returned %d from file without Engine export, want 0", n)
	}
}

func TestLoadDirSkipsNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte(`# Not an engine`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "1.0.0" },
	WResolve: func(key string) (string, error) { return "v", nil },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("LoadDir returned %d, want 1 (only .go files count)", n)
	}
}

func TestLoadDirSubdirectoriesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "nested" },
	WVersion: func() string { return "1.0.0" },
	WResolve: func(key string) (string, error) { return "v", nil },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("LoadDir returned %d, want 0  --  subdirectories must not be traversed", n)
	}
}

// ── Adversarial tests ────────────────────────────────────────────────

func TestLoadDirRefusesEngineWithNilFunctionFields(t *testing.T) {
	// A hand-built _Engine with nil method fields must be refused at load.
	// It previously loaded and answered ("", nil) from a nil WResolve, so a
	// malformed engine returned an empty secret and reported success  --  a
	// silent wrong answer is worse for a secret lookup than a refusal.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nilfuncs.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    nil,
	WVersion: nil,
	WResolve: nil,
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("LoadDir returned %d, want 0 (an engine missing methods must be refused)", n)
	}
}

func TestLoadDirSkipsEngineWithWrongExportType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wrongtype.go"), []byte(`package main

var Engine = 42
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("LoadDir returned %d from wrong-type export, want 0", n)
	}
}

func TestLoadDirSkipsEngineWithOnlySomeInterfaceMethods(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partial.go"), []byte(`package main

type partial struct{}
func (p partial) Name() string { return "partial" }
func (p partial) Version() string { return "1.0.0" }
// No Resolve() method  --  deliberately incomplete.

var Engine partial
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("LoadDir returned %d from partial impl, want 0", n)
	}
}

func TestLoadDirEngineWithPanicInResolve(t *testing.T) {
	// An engine whose Resolve() panics must still be loadable  --  the
	// panic only fires on call, not on load. Registry.Resolve does not
	// itself recover; callers that invoke a loaded engine's Resolve are
	// expected to use yaegiutil.Safe if they need panic recovery, same
	// as internal/plugin callers.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panicky.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "panicky" },
	WVersion: func() string { return "1.0.0" },
	WResolve: func(key string) (string, error) { panic("boom") },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("LoadDir returned %d, want 1 (panics are deferred to call time)", n)
	}

	e, _ := r.Get("panicky")
	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Error("Resolve() did not panic  --  expected panic from panicky engine")
			}
		}()
		_, _ = e.Resolve("k")
	}()
}

func TestLoadDirMultipleEnginesSortedByFilename(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01-alpha.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "alpha" },
	WVersion: func() string { return "1.0.0" },
	WResolve: func(key string) (string, error) { return "v", nil },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "02-beta.go"), []byte(`package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "beta" },
	WVersion: func() string { return "2.0.0" },
	WResolve: func(key string) (string, error) { return "v", nil },
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := secret.NewRegistry()
	n, err := r.LoadDir(dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("LoadDir returned %d, want 2", n)
	}
	if _, ok := r.Get("alpha"); !ok {
		t.Error("alpha engine not registered")
	}
	if _, ok := r.Get("beta"); !ok {
		t.Error("beta engine not registered")
	}
}

// The former TestGeneratedWrapperHasNilGuards was retired. It pinned
// hand-added nil guards in the generated wrapper, which every regeneration
// silently dropped, and the behaviour it protected  --  a nil WResolve
// answering ("", nil)  --  was a silent wrong answer. LoadDir now refuses such
// an engine outright; see TestLoadDirRefusesEngineWithNilFunctionFields.
