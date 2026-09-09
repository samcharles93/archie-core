package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
)

// pushHeadBranch pushes a "feature/widget" branch carrying widget.go onto the
// bare remote, giving CheckoutPR a real head to materialise.
func pushHeadBranch(t *testing.T, host string) {
	t.Helper()
	cloneDir := filepath.Join(t.TempDir(), "clone")
	r, err := git.PlainClone(cloneDir, &git.CloneOptions{URL: filepath.Join(host, "acme", "todo.git")})
	if err != nil {
		t.Fatalf("clone bare remote: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/widget"),
		Create: true,
	}); err != nil {
		t.Fatalf("create feature branch: %v", err)
	}
	writeSeedCommit(t, r, cloneDir, "widget.go", "package widget\n", "add widget")
	if err := r.Push(&git.PushOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []gitconfig.RefSpec{"refs/heads/feature/widget:refs/heads/feature/widget"},
	}); err != nil {
		t.Fatalf("push feature branch: %v", err)
	}
}

func TestCheckoutPRMaterializesHeadForDiffAndSnapshot(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	pushHeadBranch(t, host)
	m := newManager(t, host)

	dir, cleanup, err := m.CheckoutPR(ctx, "acme", "todo", "feature/widget", "main")
	if err != nil {
		t.Fatalf("CheckoutPR error = %v", err)
	}

	diff, err := m.Diff(ctx, dir, "main")
	if err != nil {
		t.Fatalf("Diff error = %v", err)
	}
	if !strings.Contains(diff, "widget.go") {
		t.Errorf("Diff = %q, want the widget.go change", diff)
	}

	snapshotDir := filepath.Join(t.TempDir(), "snap")
	if err := m.Snapshot(ctx, dir, snapshotDir); err != nil {
		t.Fatalf("Snapshot error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(snapshotDir, "widget.go"))
	if err != nil {
		t.Fatalf("read snapshotted widget.go: %v", err)
	}
	if string(content) != "package widget\n" {
		t.Errorf("snapshotted widget.go = %q, want %q", content, "package widget\n")
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("CheckoutPR dir %q still exists after cleanup: %v", dir, err)
	}
}

func TestCheckoutPRRefusesMissingHead(t *testing.T) {
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	_, cleanup, err := m.CheckoutPR(context.Background(), "acme", "todo", "feature/nonexistent", "main")
	if err == nil {
		t.Fatal("CheckoutPR error = nil, want failure for a head branch that is not pushed")
	}
	if cleanup != nil {
		t.Error("cleanup should be nil when CheckoutPR fails before materialising")
	}
}

func TestCheckoutPRRefusesMissingBase(t *testing.T) {
	host := newLocalRemote(t, "acme", "todo")
	pushHeadBranch(t, host)
	m := newManager(t, host)

	_, cleanup, err := m.CheckoutPR(context.Background(), "acme", "todo", "feature/widget", "no-such-base")
	if err == nil {
		t.Fatal("CheckoutPR error = nil, want failure for an unresolvable base branch")
	}
	// On failure CheckoutPR cleans up internally and returns a nil cleanup,
	// matching the missing-head path: cleanup is non-nil only on success.
	if cleanup != nil {
		t.Error("cleanup should be nil on failure; CheckoutPR removes the clone itself")
	}
}
