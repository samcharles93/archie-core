package pairing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicCreatesFileWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.json")

	if err := writeFileAtomic(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("content = %q", got)
	}
}

func TestWriteFileAtomicSetsMode0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approved.json")

	if err := writeFileAtomic(path, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 0600", perm)
	}
}

func TestWriteFileAtomicOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rate_limits.json")

	if err := writeFileAtomic(path, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":2}` {
		t.Errorf("content = %q, want the second write's content", got)
	}
}

func TestWriteFileAtomicLeavesNoTempFileBehindOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.json")

	if err := writeFileAtomic(path, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "pending.json" {
		t.Errorf("dir contents = %v, want exactly [pending.json] (no leftover temp file)", entries)
	}
}

func TestWriteFileAtomicFailsForNonexistentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "pending.json")
	if err := writeFileAtomic(path, []byte(`{}`)); err == nil {
		t.Fatal("expected an error writing into a nonexistent directory")
	}
}
