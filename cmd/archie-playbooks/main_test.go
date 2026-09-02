package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunUnknownCommandExits2: an unrecognised subcommand is a usage error.
func TestRunUnknownCommandExits2(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"frobnicate"}, &stderr); code != 2 {
		t.Fatalf("run(unknown) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q, want unknown-command message", stderr.String())
	}
}

// TestRunNoCommandExits2: no subcommand is a usage error with the command
// table printed (the gopls-shaped dispatch surface).
func TestRunNoCommandExits2(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code != 2 {
		t.Fatalf("run(nil) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "lint") {
		t.Fatalf("stderr = %q, want lint listed in usage", stderr.String())
	}
}

// TestRunLintCleanExits0: lint over a valid directory exits 0.
func TestRunLintCleanExits0(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "routes.yaml"), "bug: tdd\n")

	var stderr bytes.Buffer
	if code := run([]string{"lint", "-dir", dir}, &stderr); code != 0 {
		t.Fatalf("run(lint clean) = %d, want 0; stderr=%q", code, stderr.String())
	}
}

// TestRunLintCollisionExits1: a cross-directory collision exits 1 and names
// the colliding key.
func TestRunLintCollisionExits1(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirA, "a.yaml"), "security: security-review\n")
	writeFile(t, filepath.Join(dirB, "b.yaml"), "security: other-security\n")

	var stderr bytes.Buffer
	if code := run([]string{"lint", "-dir", dirA, "-dir", dirB}, &stderr); code != 1 {
		t.Fatalf("run(lint collision) = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "security") {
		t.Fatalf("stderr = %q, want the colliding label named", stderr.String())
	}
}

// TestRunLintNoDirsExits2: lint without any -dir is a usage error.
func TestRunLintNoDirsExits2(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"lint"}, &stderr); code != 2 {
		t.Fatalf("run(lint no dirs) = %d, want 2", code)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
