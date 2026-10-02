package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// headHash reads a worktree's HEAD commit, so a test can assert which commit a
// prepare landed on.
func headHash(t *testing.T, dir string) plumbing.Hash {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen(%s): %v", dir, err)
	}
	ref, err := r.Head()
	if err != nil {
		t.Fatalf("Head(%s): %v", dir, err)
	}
	return ref.Hash()
}

// prepareResume models what a task retry starts from: a fresh prepare, a commit
// pushed onto the task branch, and the pushed tip. It returns the worktree
// directory, branch and tip.
func prepareResume(t *testing.T, m *Manager, issue int, title string) (dir, branch string, tip plumbing.Hash) {
	t.Helper()
	ctx := context.Background()
	dir, branch, err := m.Prepare(ctx, "acme", "todo", testBase, issue, title, "", "bug", Fresh)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	writeSeedCommit(t, r, dir, "widget.go", "package widget\n", "add widget")
	if err := m.Push(ctx, dir, branch); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	return dir, branch, headHash(t, dir)
}

// TestPrepareResumeLandsOnTheBranchTipNotBase pins the resume target's
// semantics: it must land the worktree on the PR branch's remote tip (the
// committed work), not reset to base the way a fresh prepare does.
func TestPrepareResumeLandsOnTheBranchTipNotBase(t *testing.T) {
	ctx := context.Background()
	m := newManager(t, newLocalRemote(t, "acme", "todo"))
	dir, branch, tip := prepareResume(t, m, 7, "feat: add widget")

	if _, _, err := m.Prepare(ctx, "acme", "todo", testBase, 7, "feat: add widget", "", "bug", Target(branch)); err != nil {
		t.Fatalf("Prepare(resume) error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "widget.go"))
	if err != nil {
		t.Fatalf("widget.go missing after resume: %v (resume reset to base and discarded the branch work)", err)
	}
	if string(content) != "package widget\n" {
		t.Fatalf("widget.go = %q, want %q", content, "package widget\n")
	}
	if got := headHash(t, dir); got != tip {
		t.Fatalf("HEAD = %s, want the branch tip %s", got, tip)
	}
}

// TestPrepareResumeClonesAMissingWorktree is the root-cause test for
// archie-core-866m: a resume must create the clone when it is missing, so a
// retry reaches a task whose worktree the terminal cleanup removed, an expired
// volume dropped, or a different host never had.
func TestPrepareResumeClonesAMissingWorktree(t *testing.T) {
	ctx := context.Background()
	m := newManager(t, newLocalRemote(t, "acme", "todo"))
	dir, branch, tip := prepareResume(t, m, 8, "fix: thing")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, preparedSentinel)); !os.IsNotExist(err) {
		t.Fatalf("worktree not removed: sentinel stat err = %v", err)
	}

	gotDir, gotBranch, err := m.Prepare(ctx, "acme", "todo", testBase, 8, "fix: thing", "", "bug", Target(branch))
	if err != nil {
		t.Fatalf("Prepare(resume) on a missing worktree error = %v, want it cloned", err)
	}
	if gotDir != dir || gotBranch != branch {
		t.Fatalf("Prepare(resume) = (%q, %q), want (%q, %q)", gotDir, gotBranch, dir, branch)
	}
	content, err := os.ReadFile(filepath.Join(gotDir, "widget.go"))
	if err != nil {
		t.Fatalf("widget.go missing after resume cloned: %v", err)
	}
	if string(content) != "package widget\n" {
		t.Fatalf("widget.go = %q, want %q", content, "package widget\n")
	}
	if got := headHash(t, gotDir); got != tip {
		t.Fatalf("HEAD = %s, want the branch tip %s", got, tip)
	}
}

// TestPrepareResumeFailsClosedWhenTheBranchIsNotPushed pins that a resume
// never falls back to base: a branch that is not on the remote is a dispatch
// defect, and a base checkout would silently remediate the wrong tree.
func TestPrepareResumeFailsClosedWhenTheBranchIsNotPushed(t *testing.T) {
	ctx := context.Background()
	m := newManager(t, newLocalRemote(t, "acme", "todo"))
	dir, _, tip := prepareResume(t, m, 9, "fix: thing")

	if _, _, err := m.Prepare(ctx, "acme", "todo", testBase, 9, "fix: thing", "", "bug", Target("fix/9-never-pushed")); err == nil {
		t.Fatal("Prepare(resume) with an unpushed branch = nil error, want a fail-closed error")
	}
	if got := headHash(t, dir); got != tip {
		t.Fatalf("HEAD = %s, want the worktree left on the branch tip %s", got, tip)
	}
}

// TestPrepareResumeCleansAbandonedFiles ensures a resume clears untracked
// residue the way a fresh prepare does, so a prior interrupted run cannot leak
// files into the next commit.
func TestPrepareResumeCleansAbandonedFiles(t *testing.T) {
	ctx := context.Background()
	m := newManager(t, newLocalRemote(t, "acme", "todo"))
	dir, branch, _ := prepareResume(t, m, 10, "fix: thing")

	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("junk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Prepare(ctx, "acme", "todo", testBase, 10, "fix: thing", "", "bug", Target(branch)); err != nil {
		t.Fatalf("Prepare(resume) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch.txt")); !os.IsNotExist(err) {
		t.Errorf("scratch.txt still exists after resume; untracked residue was not cleaned")
	}
}
