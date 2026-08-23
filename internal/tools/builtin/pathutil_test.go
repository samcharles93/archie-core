package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathConfinementToggle covers the escape hatch for deployments where the
// agent is a general-purpose operator rather than a coding assistant scoped to
// one project -- formatting and mounting a new disk, migrating data between
// filesystems, editing service units. Confining those to one directory makes
// the agent useless, so the jail has to be switchable.
//
// Not parallel: the toggle is process-wide.
func TestPathConfinementToggle(t *testing.T) {
	const workspace = "/home/sam"
	outside := []string{"/etc/fstab", "/mnt/newdisk/data", "/srv"}

	t.Run("confined by default", func(t *testing.T) {
		for _, target := range outside {
			if isConfined(workspace, target) {
				t.Errorf("isConfined(%q, %q) = true, want false by default", workspace, target)
			}
			if isReadConfined(workspace, target) {
				t.Errorf("isReadConfined(%q, %q) = true, want false by default", workspace, target)
			}
		}
	})

	t.Run("unconfined when disabled", func(t *testing.T) {
		SetPathConfinement(false)
		t.Cleanup(func() { SetPathConfinement(true) })

		for _, target := range outside {
			if !isConfined(workspace, target) {
				t.Errorf("isConfined(%q, %q) = false with confinement disabled", workspace, target)
			}
			if !isReadConfined(workspace, target) {
				t.Errorf("isReadConfined(%q, %q) = false with confinement disabled", workspace, target)
			}
		}
	})

	t.Run("restored after re-enabling", func(t *testing.T) {
		if isConfined(workspace, "/etc/fstab") {
			t.Error("confinement did not come back after being re-enabled")
		}
	})
}

// TestResolvePathUnaffectedByConfinement pins the property that makes the
// toggle safe to flip: relative paths stay rooted at the workspace either way,
// so turning the jail off widens what an ABSOLUTE path can reach without
// silently changing where a relative one lands.
func TestResolvePathUnaffectedByConfinement(t *testing.T) {
	SetPathConfinement(false)
	t.Cleanup(func() { SetPathConfinement(true) })

	if got := resolvePath("/home/sam", "notes.md"); got != "/home/sam/notes.md" {
		t.Errorf("resolvePath relative = %q, want it still rooted at the workspace", got)
	}
	if got := resolvePath("/home/sam", "/etc/fstab"); got != "/etc/fstab" {
		t.Errorf("resolvePath absolute = %q, want it untouched", got)
	}
}

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

func TestGrepRejectsGoModCache(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	withGoModCache(t, cache)

	if err := os.WriteFile(filepath.Join(cache, "dep.go"), []byte("package dep\nfunc Needle() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(cwd, nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"pattern":"Needle","path":`+quote(cache)+`}`,
	), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "escapes working directory") {
		t.Fatalf("expected grep of the module cache to be rejected, got: %#v", res)
	}
}

func TestFindRejectsOutsideWorkspace(t *testing.T) {
	cwd := t.TempDir()
	cache := t.TempDir()
	other := t.TempDir()
	withGoModCache(t, cache)

	if err := os.MkdirAll(filepath.Join(cache, "example.com", "dep@v1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFindTool(cwd)
	for _, path := range []string{cache, other, "/home/sam"} {
		res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":`+quote(path)+`}`), nil)
		if err != nil {
			t.Fatalf("path %s: unexpected error: %v", path, err)
		}
		if !res.IsError || !strings.Contains(res.Content, "escapes working directory") {
			t.Fatalf("path %s: expected workspace-confined find, got: %#v", path, res)
		}
	}

	inside := filepath.Join(cwd, "keep.go")
	if err := os.WriteFile(inside, []byte("package keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"pattern":"keep.go"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "keep.go") {
		t.Fatalf("expected workspace find to succeed, got: %#v", res)
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
		`{"path":`+quote(filepath.Join(cache, "evil.go"))+`,"content":"package evil\n"}`,
	), nil)
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
		`{"path":`+quote(target)+`,"edits":[{"old_text":"package dep","new_text":"package hacked"}]}`,
	), nil)
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
