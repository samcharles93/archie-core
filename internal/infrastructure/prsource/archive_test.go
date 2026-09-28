package prsource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// buildTarGz writes files (path -> content) into a gzipped tar stream,
// optionally wrapped in a single leading directory the way GitHub's and
// Gitea's repository archives are (e.g. "acme-widget-deadbeef/").
func buildTarGz(t *testing.T, wrapper string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, content := range files {
		full := path
		if wrapper != "" {
			full = wrapper + "/" + path
		}
		if err := tw.WriteHeader(&tar.Header{Name: full, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarGzStripsTheWrapperDirectory(t *testing.T) {
	data := buildTarGz(t, "acme-widget-deadbeef", map[string]string{
		"README.md":       "hello\n",
		"src/main.go":     "package main\n",
		"src/nested/a.go": "package nested\n",
	})
	dest := t.TempDir()

	if err := extractTarGz(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	for _, rel := range []string{"README.md", "src/main.go", "src/nested/a.go"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "acme-widget-deadbeef")); err == nil {
		t.Error("the wrapper directory itself should not survive extraction")
	}
}

func TestExtractTarGzWithNoWrapperDirectory(t *testing.T) {
	data := buildTarGz(t, "", map[string]string{"a.go": "package a\n"})
	dest := t.TempDir()

	if err := extractTarGz(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.go")); err != nil {
		t.Errorf("expected a.go to exist: %v", err)
	}
}

func TestExtractTarGzRefusesPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	evil := "wrapper/../../../etc/passwd-clone"
	if err := tw.WriteHeader(&tar.Header{Name: evil, Mode: 0o644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("evil")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	if err := extractTarGz(bytes.NewReader(buf.Bytes()), dest); err == nil {
		t.Fatal("want an error for a path-traversal archive entry, not a silent write outside destDir")
	}
}

func TestExtractTarGzLeavesNoDotGit(t *testing.T) {
	data := buildTarGz(t, "wrapper", map[string]string{
		"a.go":           "package a\n",
		".git/HEAD":      "ref: refs/heads/main\n",
		".git/config":    "[core]\n",
		".github/x.yaml": "ok\n",
	})
	dest := t.TempDir()

	if err := extractTarGz(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		t.Error("a .git directory must never survive extraction (Isolation: the pipeline reads a .git-free snapshot)")
	}
	if _, err := os.Stat(filepath.Join(dest, ".github", "x.yaml")); err != nil {
		t.Errorf("a non-.git dotfile should still extract: %v", err)
	}
}

func TestExtractTarGzStripsWrapperWithExplicitDirectoryEntries(t *testing.T) {
	// Real GitHub/Gitea archives include explicit tar directory headers for
	// the wrapper and every subdirectory, not just file entries with long
	// names -- a wrapper-only directory header has no "/" in its own
	// (cleaned) name, which must not be mistaken for "no wrapper exists".
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dirs := []string{"acme-widget-deadbeef/", "acme-widget-deadbeef/src/"}
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"acme-widget-deadbeef/README.md":   "hello\n",
		"acme-widget-deadbeef/src/main.go": "package main\n",
	}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	if err := extractTarGz(bytes.NewReader(buf.Bytes()), dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	for _, rel := range []string{"README.md", "src/main.go"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "acme-widget-deadbeef")); err == nil {
		t.Error("the wrapper directory itself should not survive extraction")
	}
}
