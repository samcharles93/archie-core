package archieplaybooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLintCleanExitsZero: a set of directories with no collisions lints
// cleanly -- exit 0, no findings.
func TestLintCleanExitsZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "routes.yaml"), "bug: tdd\n")
	writeFile(t, filepath.Join(dir, "labels.yaml"), "security: security-review\n")

	result := Lint([]string{dir}, os.Stdout)
	if result.ExitCode != 0 {
		t.Fatalf("Lint() exit = %d, want 0; findings: %#v", result.ExitCode, result.Findings)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("Lint() findings = %#v, want none", result.Findings)
	}
}

// TestLintCrossDirectoryCollisionExitsNonZero is the load-bearing case: two
// directories binding the same label is a lint failure with a clear message
// naming the colliding key, and exit non-zero.
func TestLintCrossDirectoryCollisionExitsNonZero(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirA, "a.yaml"), "security: security-review\n")
	writeFile(t, filepath.Join(dirB, "b.yaml"), "security: other-security\n")

	result := Lint([]string{dirA, dirB}, os.Stdout)
	if result.ExitCode == 0 {
		t.Fatal("Lint() exit = 0, want non-zero for collision")
	}
	if len(result.Findings) == 0 {
		t.Fatal("Lint() findings = none, want the collision reported")
	}
	joined := strings.Join(result.Findings, "\n")
	if !strings.Contains(joined, "security") {
		t.Fatalf("Lint() findings do not name the colliding label: %q", joined)
	}
}

// TestLintWithinDirectoryCollisionExitsNonZero: the more common real case --
// two files in one directory binding the same key.
func TestLintWithinDirectoryCollisionExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "security: security-review\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "security: other-security\n")

	result := Lint([]string{dir}, os.Stdout)
	if result.ExitCode == 0 {
		t.Fatal("Lint() exit = 0, want non-zero for within-dir collision")
	}
}

// TestLintMalformedFileIsReported: a malformed YAML file is a lint finding.
func TestLintMalformedFileIsReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.yaml"), "\t: : :\n")

	result := Lint([]string{dir}, os.Stdout)
	if result.ExitCode == 0 {
		t.Fatal("Lint() exit = 0, want non-zero for malformed file")
	}
}

// TestLintEmptyDirsAreClean: no directories configured (or none exist) is a
// clean lint, not an error -- matching the loaders' empty-means-defaults
// convention.
func TestLintEmptyDirsAreClean(t *testing.T) {
	result := Lint(nil, os.Stdout)
	if result.ExitCode != 0 {
		t.Fatalf("Lint(nil) exit = %d, want 0", result.ExitCode)
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	result = Lint([]string{missing}, os.Stdout)
	if result.ExitCode != 0 {
		t.Fatalf("Lint(missing dir) exit = %d, want 0", result.ExitCode)
	}
}

// TestLintReportsLoadErrorAsFinding: the linter surfaces the loader's
// definition failure verbatim -- it agrees with the daemon's startup
// validation because it IS the same validation, not a parallel one.
func TestLintReportsLoadErrorAsFinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "security: security-review\nbug: tdd\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "security: other-security\nbug: custom-bug\n")

	result := Lint([]string{dir}, os.Stdout)
	if result.ExitCode == 0 {
		t.Fatal("Lint() exit = 0, want non-zero")
	}
	joined := strings.Join(result.Findings, "\n")
	// Both "bug" and "security" collide across the two files; the loader
	// sorts a file's keys before checking them (routing.go's
	// loadPlaybookFile), so "bug" -- alphabetically first -- is always the
	// one reported, deterministically.
	if !strings.Contains(joined, "bug") {
		t.Fatalf("Lint() findings = %q, want the colliding key named", joined)
	}
	// Fail-fast like the daemon: one definition failure is reported (the
	// loader returns the first), not an enumeration of every collision.
	if len(result.Findings) != 1 {
		t.Fatalf("Lint() findings = %d, want exactly 1 (loader fail-fast contract)", len(result.Findings))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
