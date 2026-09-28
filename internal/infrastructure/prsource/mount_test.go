package prsource

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writePrefetched(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "snapshot", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"title":"t","body":"b","head_sha":"abc123"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), []byte("diff --git a/x b/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot", "file.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot", "sub", "nested.go"), []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMountSourceMetadataReadsSidecarFile(t *testing.T) {
	dir := t.TempDir()
	writePrefetched(t, dir)
	src := NewFromMount(dir)

	meta, err := src.Metadata(context.Background(), "acme", "widget", 1)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Title != "t" || meta.Body != "b" {
		t.Fatalf("meta = %+v, want title/body from the sidecar file", meta)
	}
}

func TestMountSourceDiffReadsPatchFile(t *testing.T) {
	dir := t.TempDir()
	writePrefetched(t, dir)
	src := NewFromMount(dir)

	diff, err := src.Diff(context.Background(), "acme", "widget", 1)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "diff --git a/x b/x\n" {
		t.Fatalf("diff = %q, want the patch file's content", diff)
	}
}

func TestMountSourceSnapshotCopiesFilesAndReturnsHeadSHA(t *testing.T) {
	dir := t.TempDir()
	writePrefetched(t, dir)
	src := NewFromMount(dir)
	destDir := t.TempDir()

	headSHA, err := src.Snapshot(context.Background(), "acme", "widget", 1, destDir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if headSHA != "abc123" {
		t.Fatalf("headSHA = %q, want abc123", headSHA)
	}
	got, err := os.ReadFile(filepath.Join(destDir, "file.go"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "package a\n" {
		t.Fatalf("copied file content = %q", got)
	}
	nested, err := os.ReadFile(filepath.Join(destDir, "sub", "nested.go"))
	if err != nil {
		t.Fatalf("read copied nested file: %v", err)
	}
	if string(nested) != "package sub\n" {
		t.Fatalf("copied nested file content = %q", nested)
	}
}

func TestMountSourceMetadataFailsWithoutPrefetchedData(t *testing.T) {
	src := NewFromMount(t.TempDir())
	if _, err := src.Metadata(context.Background(), "acme", "widget", 1); err == nil {
		t.Fatal("want an error when no metadata.json was prefetched")
	}
}
