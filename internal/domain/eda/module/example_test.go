package module

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestShippedExampleModuleLoads verifies the operator-facing example
// (examples/modules/log.go) loads and runs through the real registry -- the
// proof that the shipped contract works outside test fixtures.
func TestShippedExampleModuleLoads(t *testing.T) {
	// Find the repo root (examples/modules/log.go) relative to this package.
	dir := filepath.Join("../../../..", "examples", "modules")
	if _, err := os.Stat(filepath.Join(dir, "log.go")); err != nil {
		t.Skipf("example module not present at %s: %v", dir, err)
	}

	r := New()
	if err := r.Register("log", dir); err != nil {
		t.Fatalf("Register(shipped example) = %v", err)
	}
	res, err := r.Invoke(context.Background(), "log", map[string]any{"message": "build finished"})
	if err != nil {
		t.Fatalf("Invoke(shipped example) = %v", err)
	}
	if res["written"] != true {
		t.Errorf("written = %v, want true", res["written"])
	}
	if res["level"] != "info" {
		t.Errorf("level = %v, want default info", res["level"])
	}
}
