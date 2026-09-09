package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v6"
)

// TestResumeResetsToBranchTipNotBase pins the remediate workflow's worktree
// semantics: Resume must land the worktree on the PR branch's remote tip (the
// committed work), not reset to base the way refresh does for a fresh run.
func TestResumeResetsToBranchTipNotBase(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, branch, err := m.Prepare(ctx, "acme", "todo", testBase, 7, "feat: add widget", "", "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Commit + push work on the branch, so origin/<branch> has widget.go
	// while base (main) has only README.
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	writeSeedCommit(t, r, dir, "widget.go", "package widget\n", "add widget")
	if err := m.Push(ctx, dir, branch); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	if err := m.Resume(ctx, dir, branch); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "widget.go"))
	if err != nil {
		t.Fatalf("widget.go missing after Resume: %v (resume reset to base and discarded the branch work)", err)
	}
	if string(content) != "package widget\n" {
		t.Fatalf("widget.go = %q, want %q", content, "package widget\n")
	}
}

// TestResumeCleansAbandonedFiles ensures Resume clears untracked residue the
// way refresh does, so a prior interrupted remediation cannot leak files into
// the next run.
func TestResumeCleansAbandonedFiles(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, branch, err := m.Prepare(ctx, "acme", "todo", testBase, 8, "fix: thing", "", "bug")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// The branch must be pushed for Resume to resolve origin/<branch>.
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	writeSeedCommit(t, r, dir, "change.go", "package change\n", "a change")
	if err := m.Push(ctx, dir, branch); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	// Untracked residue that a clean run must remove.
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("junk\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Resume(ctx, dir, branch); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch.txt")); !os.IsNotExist(err) {
		t.Errorf("scratch.txt still exists after Resume; untracked residue was not cleaned")
	}
}
