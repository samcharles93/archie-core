package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunHonoursAbsoluteOutputPath(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	outputDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for _, name := range []string{"first.yaml", "second.yaml"} {
		output := filepath.Join(outputDir, name)
		if err := run(repoRoot, output); err != nil {
			t.Fatalf("run(%q) error = %v", name, err)
		}
		if _, err := os.Stat(output); err != nil {
			t.Fatalf("generated output %q: %v", name, err)
		}
	}
	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get restored working directory: %v", err)
	}
	if currentDir != originalDir {
		t.Fatalf("working directory = %q, want %q", currentDir, originalDir)
	}
}
