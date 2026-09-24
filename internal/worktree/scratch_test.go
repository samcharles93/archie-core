package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchWorkspaceStartsEmptyAndStaysOutOfWorktrees(t *testing.T) {
	m := &Manager{WorkDir: t.TempDir()}
	dir, err := m.PrepareScratch(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "left-over"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := m.PrepareScratch(7)
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(again); len(entries) != 0 {
		t.Fatalf("second attempt's workspace holds %d entries, want an empty one", len(entries))
	}
	if strings.HasPrefix(again, m.Dir("scratch", "x", 7)) {
		t.Fatalf("scratch dir %q lies inside a worktree path", again)
	}
	if err := m.RemoveScratch(7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(again); !os.IsNotExist(err) {
		t.Fatalf("scratch dir still exists after RemoveScratch: %v", err)
	}
}
