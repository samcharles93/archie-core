package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withGoModCache points the module-cache lookup at a temp dir for the duration
// of a test, so these tests never depend on the host's real GOMODCACHE.
func withGoModCache(t *testing.T, dir string) {
	t.Helper()
	prev := goModCacheDir
	goModCacheDir = func() string { return dir }
	t.Cleanup(func() { goModCacheDir = prev })
}

func TestReadAllowsGoModCache(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	withGoModCache(t, cache)

	dep := filepath.Join(cache, "example.com", "dep@v1.0.0")
	if err := os.MkdirAll(dep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dep, "dep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := execRead(t, cwd, `{"path":`+quote(filepath.Join(dep, "dep.go"))+`}`)
	if res.IsError {
		t.Fatalf("expected read of a module-cache file to succeed, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "package dep") {
		t.Fatalf("unexpected content: %s", res.Content)
	}
}

func TestGrepAllowsGoModCache(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	withGoModCache(t, cache)

	if err := os.WriteFile(filepath.Join(cache, "dep.go"), []byte("package dep\nfunc Needle() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(cwd, nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"pattern":"Needle","path":`+quote(cache)+`}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected grep of the module cache to succeed, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Needle") {
		t.Fatalf("unexpected content: %s", res.Content)
	}
}

// The module cache is a read-only escape hatch. Mutating tools must still be
// confined to the workspace, or the agent could corrupt shared dependencies.
func TestWriteRejectsGoModCache(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	withGoModCache(t, cache)

	tool := NewWriteTool(cwd, nil, NewReadTracker())
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":`+quote(filepath.Join(cache, "evil.go"))+`,"content":"package evil\n"}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "escapes working directory") {
		t.Fatalf("expected write to the module cache to be rejected, got: %#v", res)
	}
}

func TestEditRejectsGoModCache(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	withGoModCache(t, cache)

	target := filepath.Join(cache, "dep.go")
	if err := os.WriteFile(target, []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := NewReadTracker()
	rt.MarkRead(cwd, target)
	tool := NewEditTool(cwd, nil, rt)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":`+quote(target)+`,"edits":[{"old_text":"package dep","new_text":"package hacked"}]}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "escapes working directory") {
		t.Fatalf("expected edit of the module cache to be rejected, got: %#v", res)
	}
}

// A path in neither the workspace nor the module cache stays rejected.
func TestReadRejectsUnrelatedAbsolutePath(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	other := t.TempDir()
	withGoModCache(t, cache)

	target := filepath.Join(other, "secret.txt")
	if err := os.WriteFile(target, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := execRead(t, cwd, `{"path":`+quote(target)+`}`)
	if !res.IsError || !strings.Contains(res.Content, "escapes working directory") {
		t.Fatalf("expected an unrelated absolute path to be rejected, got: %#v", res)
	}
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
