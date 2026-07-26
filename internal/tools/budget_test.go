package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultTurnBudgetChars(t *testing.T) {
	if DefaultTurnBudgetChars != 200_000 {
		t.Errorf("DefaultTurnBudgetChars = %d, want 200000", DefaultTurnBudgetChars)
	}
}

func TestTurnBudgetConsume(t *testing.T) {
	b := NewTurnBudget(1000, "")

	// Consume within budget.
	if !b.Consume(500) {
		t.Error("expected consume to succeed")
	}
	if b.Used() != 500 {
		t.Errorf("Used = %d, want 500", b.Used())
	}
	if b.Remaining() != 500 {
		t.Errorf("Remaining = %d, want 500", b.Remaining())
	}
	if b.Exceeded() {
		t.Error("should not be exceeded")
	}

	// Consume more within budget.
	if !b.Consume(400) {
		t.Error("expected consume to succeed")
	}

	// This exceeds the budget.
	if b.Consume(200) {
		t.Error("expected consume to fail")
	}
	if !b.Exceeded() {
		t.Error("should be exceeded")
	}
	if b.Remaining() != 0 {
		t.Errorf("Remaining = %d, want 0", b.Remaining())
	}

	// Subsequent consumes always fail.
	if b.Consume(1) {
		t.Error("consume should fail after exceeded")
	}
}

func TestTurnBudgetExactBoundary(t *testing.T) {
	// At exactly MaxChars, should succeed. Exceeded only above MaxChars.
	b := NewTurnBudget(100, "")
	if !b.Consume(99) {
		t.Error("99/100 should succeed")
	}
	if !b.Consume(1) {
		t.Error("100/100 should succeed (exactly at limit)")
	}
	if b.Exceeded() {
		t.Error("should not be exceeded at exactly the limit")
	}
	// One more char triggers exceeded.
	if b.Consume(1) {
		t.Error("101/100 should be exceeded")
	}
	if !b.Exceeded() {
		t.Error("should be exceeded above the limit")
	}
}

func TestTurnBudgetSpillToDisk(t *testing.T) {
	dir := t.TempDir()
	b := NewTurnBudget(100, dir)

	// Spill some content.
	content := []byte(strings.Repeat("x", 200))
	ref := b.Spill("my-tool", content)

	if ref.SizeChars != 200 {
		t.Errorf("SizeChars = %d, want 200", ref.SizeChars)
	}
	if ref.Path == "" {
		t.Error("spill path should not be empty")
	}
	if !strings.HasPrefix(ref.Path, dir) {
		t.Errorf("spill path %q should be under %q", ref.Path, dir)
	}

	// Verify file exists and has correct content.
	got, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("spill content mismatch: got %q, want %q", string(got), string(content))
	}

	// Budget should be exceeded after spill.
	if !b.Exceeded() {
		t.Error("budget should be exceeded after spilling 200 chars into 100 budget")
	}
}

func TestTurnBudgetSpillNoDir(t *testing.T) {
	b := NewTurnBudget(100, "")

	ref := b.Spill("tool", []byte("hello"))
	if ref.Path != "" {
		t.Error("spill path should be empty when spillDir is empty")
	}
	if ref.SizeChars != 5 {
		t.Errorf("SizeChars = %d, want 5", ref.SizeChars)
	}
}

func TestTurnBudgetSpilled(t *testing.T) {
	b := NewTurnBudget(1000, "")

	b.Spill("a", []byte("aa"))
	b.Spill("b", []byte("bb"))

	spilled := b.Spilled()
	if len(spilled) != 2 {
		t.Errorf("expected 2 spills, got %d", len(spilled))
	}
	if spilled[0].SizeChars != 2 {
		t.Errorf("first spill size = %d, want 2", spilled[0].SizeChars)
	}
	if spilled[1].SizeChars != 2 {
		t.Errorf("second spill size = %d, want 2", spilled[1].SizeChars)
	}
}

func TestTurnBudgetReset(t *testing.T) {
	b := NewTurnBudget(100, "")
	b.Consume(99)
	b.Spill("tool", []byte("overflow"))

	if !b.Exceeded() {
		t.Fatal("expected exceeded")
	}

	b.Reset()

	if b.Exceeded() {
		t.Error("after reset, should not be exceeded")
	}
	if b.Used() != 0 {
		t.Errorf("Used = %d, want 0", b.Used())
	}
	if b.Remaining() != 100 {
		t.Errorf("Remaining = %d, want 100", b.Remaining())
	}
	if len(b.Spilled()) != 0 {
		t.Errorf("Spilled = %d, want 0", len(b.Spilled()))
	}
}

func TestTurnBudgetConcurrent(t *testing.T) {
	b := NewTurnBudget(100_000, "")

	done := make(chan struct{})
	for range 10 {
		go func() {
			for range 100 {
				b.Consume(10)
				b.Used()
				b.Remaining()
				b.Exceeded()
			}
			done <- struct{}{}
		}()
	}

	for range 10 {
		<-done
	}

	// No panic and budget was consumed.
	if b.Used() < 1000 {
		t.Errorf("Used = %d, expected at least 1000", b.Used())
	}
}

func TestTurnBudgetSpillFileNaming(t *testing.T) {
	dir := t.TempDir()
	b := NewTurnBudget(100_000, dir)

	b.Spill("my-awesome-tool", []byte("data"))

	spilled := b.Spilled()
	if len(spilled) != 1 {
		t.Fatalf("expected 1 spill, got %d", len(spilled))
	}

	// File name should contain the tool name.
	base := filepath.Base(spilled[0].Path)
	if !strings.Contains(base, "my-awesome-tool") {
		t.Errorf("spill file name %q should contain tool name", base)
	}
}

func TestTurnBudgetSpillDirPermissions(t *testing.T) {
	// Create a regular file and use a sub-path as the spill directory.
	// Even root cannot write a file when a path component is a regular
	// file, making this test reliable regardless of privileges.
	f, err := os.CreateTemp("", "archie-test-spill-blocker")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	// spillDir is a sub-path of the regular file — WriteFile must fail.
	spillDir := filepath.Join(f.Name(), "sub")

	b := NewTurnBudget(100_000, spillDir)
	ref := b.Spill("tool", []byte("data"))

	// Spill should handle write failure gracefully.
	if ref.Path != "" {
		t.Errorf("spill path should be empty on write failure, got %q", ref.Path)
	}
	if ref.SizeChars != 4 {
		t.Errorf("SizeChars should still be recorded: got %d", ref.SizeChars)
	}
}
