package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/samcharles93/archie-core/internal/container"
)

const testBase = "main"

func testSignature() *object.Signature {
	return &object.Signature{
		Name:  "seeder",
		Email: "seed@example.com",
		When:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// newLocalRemote creates a bare repo with one commit on main and returns
// the directory that serves as the fake forge host: the bare repo lives
// at <host>/<owner>/<repo>.git so cloneURL resolves it.
//
// Built with go-git rather than by shelling out, so the suite no longer
// requires a git binary on the machine running it.
func newLocalRemote(t *testing.T, owner, repo string) string {
	t.Helper()
	host := t.TempDir()
	bare := filepath.Join(host, owner, repo+".git")

	if _, err := git.PlainInit(bare, true, git.WithDefaultBranch(
		plumbing.NewBranchReferenceName(testBase),
	)); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	seed := filepath.Join(t.TempDir(), "seed")
	sr, err := git.PlainInit(seed, false, git.WithDefaultBranch(
		plumbing.NewBranchReferenceName(testBase),
	))
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	writeSeedCommit(t, sr, seed, "README.md", "seed\n", "seed")

	if _, err := sr.CreateRemote(&gitconfig.RemoteConfig{
		Name: git.DefaultRemoteName,
		URLs: []string{bare},
	}); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	ref := plumbing.NewBranchReferenceName(testBase)
	if err := sr.Push(&git.PushOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec(ref + ":" + ref)},
	}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	return host
}

func writeSeedCommit(t *testing.T, r *git.Repository, dir, name, content, message string) plumbing.Hash {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	disableSigning(t, r)
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	sig := testSignature()
	hash, err := wt.Commit(message, &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash
}

// disableSigning mirrors what Manager.setIdentity does for real clones,
// so fixtures do not inherit commit.gpgSign from the host's global config.
func disableSigning(t *testing.T, r *git.Repository) {
	t.Helper()
	cfg, err := r.Config()
	if err != nil {
		t.Fatalf("read fixture config: %v", err)
	}
	cfg.Raw.Section("commit").SetOption("gpgsign", "false")
	if err := r.SetConfig(cfg); err != nil {
		t.Fatalf("write fixture config: %v", err)
	}
}

func newManager(t *testing.T, host string) *Manager {
	t.Helper()
	return &Manager{
		WorkDir:  t.TempDir(),
		BotUser:  "archie-bot",
		BotEmail: "archie@example.com",
		BaseURL:  host,
	}
}

func TestPrepareCommitPushRoundTrip(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, branch, err := m.Prepare(ctx, "acme", "todo", testBase, 7, "feat: add widget", "", "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !strings.HasPrefix(branch, "feat/7-") {
		t.Errorf("branch = %q, want prefix feat/7-", branch)
	}
	if _, err := os.Stat(filepath.Join(dir, preparedSentinel)); err != nil {
		t.Errorf("prepared sentinel missing: %v", err)
	}

	// A clean tree must report "nothing committed" rather than erroring.
	committed, err := m.CommitAll(ctx, dir, "no-op")
	if err != nil {
		t.Fatalf("CommitAll() on clean tree error = %v", err)
	}
	if committed {
		t.Error("CommitAll() = true on a clean tree, want false")
	}

	if err := os.WriteFile(filepath.Join(dir, "widget.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	committed, err = m.CommitAll(ctx, dir, "feat: add widget")
	if err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}
	if !committed {
		t.Fatal("CommitAll() = false, want true after adding a file")
	}

	files, err := m.ChangedFiles(ctx, dir, testBase)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	if len(files) != 1 || files[0] != "widget.txt" {
		t.Errorf("ChangedFiles() = %v, want [widget.txt]", files)
	}

	lines, err := m.ChangedLines(ctx, dir, testBase)
	if err != nil {
		t.Fatalf("ChangedLines() error = %v", err)
	}
	if lines != 2 {
		t.Errorf("ChangedLines() = %d, want 2", lines)
	}

	diff, err := m.Diff(ctx, dir, testBase)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	if !strings.Contains(diff, "widget.txt") {
		t.Errorf("Diff() does not mention the changed file:\n%s", diff)
	}

	if err := m.Push(ctx, dir, branch); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	// The branch must actually exist on the remote afterwards.
	bare := filepath.Join(host, "acme", "todo.git")
	remote, err := git.PlainOpen(bare)
	if err != nil {
		t.Fatalf("open bare remote: %v", err)
	}
	if _, err := remote.Reference(plumbing.NewBranchReferenceName(branch), true); err != nil {
		t.Errorf("branch %q not present on remote after Push: %v", branch, err)
	}
}

func TestPrepareIsIdempotent(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir1, branch1, err := m.Prepare(ctx, "acme", "todo", testBase, 3, "fix: thing", "", "bug")
	if err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	// A marker file proves the second call reused the clone instead of
	// wiping and re-cloning it.
	marker := filepath.Join(dir1, ".git", "reuse-marker")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	dir2, branch2, err := m.Prepare(ctx, "acme", "todo", testBase, 3, "fix: thing", "", "bug")
	if err != nil {
		t.Fatalf("second Prepare() error = %v", err)
	}
	if dir1 != dir2 || branch1 != branch2 {
		t.Errorf("Prepare() not stable: (%q,%q) then (%q,%q)", dir1, branch1, dir2, branch2)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("second Prepare() re-cloned instead of reusing the prepared worktree")
	}
}

// TestPrepareRecoversFromADirtyWorktreeOnRetry reproduces a real retry
// failure seen in production: a task that got interrupted mid-stage (killed
// container, or a stage that edits files before ever reaching commit)
// leaves uncommitted local changes -- a modified tracked file plus an
// untracked one -- sitting in the worktree. HEAD is already on the correct
// branch (this is not the "no sentinel" case above). refresh's checkout,
// without Force, resets in git's MergeReset mode, which can fail to
// reconcile a working tree that differs from HEAD even when the target
// commit hasn't moved -- and did fail here with
// `checkout branch <branch>: a branch named "refs/heads/<branch>" already
// exists`, since the fallback that mis-assumes "checkout failed means the
// branch doesn't exist yet" then collides with the branch that, in fact,
// already exists. Retry must recover from this on its own; a worktree that
// refresh is about to hard-reset to base has no reason to fail here at all.
func TestPrepareRecoversFromADirtyWorktreeOnRetry(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	seedDir := filepath.Join(t.TempDir(), "ignore-seed")
	seedRepo, err := git.PlainClone(seedDir, &git.CloneOptions{URL: filepath.Join(host, "acme", "todo.git")})
	if err != nil {
		t.Fatal(err)
	}
	writeSeedCommit(t, seedRepo, seedDir, ".gitignore", "bin/\n", "chore: ignore build output")
	if err := os.Symlink("README.md", filepath.Join(seedDir, "readme-link")); err != nil {
		t.Fatal(err)
	}
	seedWorktree, err := seedRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedWorktree.Add("readme-link"); err != nil {
		t.Fatal(err)
	}
	sig := testSignature()
	if _, err := seedWorktree.Commit("chore: add tracked symlink", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatal(err)
	}
	baseRef := plumbing.NewBranchReferenceName(testBase)
	if err := seedRepo.Push(&git.PushOptions{RemoteName: git.DefaultRemoteName, RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(baseRef + ":" + baseRef)}}); err != nil {
		t.Fatal(err)
	}
	m := newManager(t, host)

	dir, branch, err := m.Prepare(ctx, "acme", "todo", testBase, 9, "fix: retry robustness", "", "bug")
	if err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}

	// Simulate an interrupted attempt: a tracked file edited but never
	// committed, plus a new file the agent created and never added.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\nedited, uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("uncommitted new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "stale"), []byte("ignored output\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir2, branch2, err := m.Prepare(ctx, "acme", "todo", testBase, 9, "fix: retry robustness", "", "bug")
	if err != nil {
		t.Fatalf("retry Prepare() on a dirty worktree error = %v, want recovery", err)
	}
	if dir2 != dir || branch2 != branch {
		t.Errorf("retry Prepare() = (%q, %q), want the same (%q, %q)", dir2, branch2, dir, branch)
	}

	// Retry starts from the base commit: neither tracked edits nor untracked
	// output abandoned by the previous attempt may survive into the commit
	// that CommitAll will publish for this attempt.
	content, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "seed\n" {
		t.Errorf("README.md = %q, want the base commit's content restored", content)
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch.txt")); !os.IsNotExist(err) {
		t.Fatalf("abandoned untracked file survived retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "stale")); !os.IsNotExist(err) {
		t.Fatalf("ignored abandoned file survived retry: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "readme-link")); err != nil || target != "README.md" {
		t.Fatalf("tracked symlink = (%q, %v), want README.md", target, err)
	}
	if _, err := os.Stat(filepath.Join(dir, preparedSentinel)); err != nil {
		t.Fatalf("retry removed prepared sentinel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "intended.txt"), []byte("new attempt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := m.CommitAll(ctx, dir, "feat: intended retry change"); err != nil || !changed {
		t.Fatalf("CommitAll() = (%v, %v), want a commit", changed, err)
	}
	files, err := m.ChangedFiles(ctx, dir, testBase)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "intended.txt" {
		t.Fatalf("ChangedFiles() = %v, want only intended.txt", files)
	}
}

// A repository whose .git is a gitdir *file* rather than a directory (a
// linked worktree, or a clone adopted by migrateLegacy) must still have its
// abandoned untracked files cleaned. Returning filepath.SkipDir for a
// non-directory aborts the remaining entries of its parent -- at the
// worktree root that is the entire walk, so cleaning silently did nothing
// and CommitAll published the leftovers on the next attempt.
func TestCleanUntrackedHandlesGitdirFile(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, _, err := m.Prepare(ctx, "acme", "todo", testBase, 12, "fix: gitdir file", "", "bug")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Relocate the repository metadata and leave a gitdir pointer behind.
	gitDir := filepath.Join(t.TempDir(), "detached-gitdir")
	if err := os.Rename(filepath.Join(dir, ".git"), gitDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Sorts after ".git", so it is only reached if the walk continues.
	abandoned := filepath.Join(dir, "scratch.txt")
	if err := os.WriteFile(abandoned, []byte("abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen() with a gitdir file error = %v", err)
	}
	if err := cleanUntracked(r, dir); err != nil {
		t.Fatalf("cleanUntracked() error = %v", err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("abandoned untracked file survived cleaning: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("gitdir pointer file was removed: %v", err)
	}
}

func TestCollectTrackedPathsClassifiesGitEntries(t *testing.T) {
	files := make(map[string]struct{})
	dirs := make(map[string]struct{})
	opaque := make(map[string]struct{})
	tree := &object.Tree{Entries: []object.TreeEntry{
		{Name: "file", Mode: filemode.Regular},
		{Name: "link", Mode: filemode.Symlink},
		{Name: "dependency", Mode: filemode.Submodule},
	}}
	if err := collectTrackedPaths(tree, "", files, dirs, opaque); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file", "link"} {
		if _, ok := files[name]; !ok {
			t.Errorf("tracked file %q was not classified", name)
		}
	}
	if _, ok := opaque["dependency"]; !ok {
		t.Error("submodule was not classified as an opaque directory")
	}
}

// An interrupted clone leaves a directory with no sentinel. Prepare must
// discard it rather than trying to reuse a half-built worktree.
func TestPrepareDiscardsDirectoryWithoutSentinel(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir := m.Dir("acme", "todo", 11)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "half-written")
	if err := os.WriteFile(junk, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.Prepare(ctx, "acme", "todo", testBase, 11, "chore: retry", "", "chore"); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Error("Prepare() reused an interrupted clone instead of discarding it")
	}
	if _, err := os.Stat(filepath.Join(dir, preparedSentinel)); err != nil {
		t.Errorf("prepared sentinel missing after recovery: %v", err)
	}
}

func TestPrepareErrors(t *testing.T) {
	host := newLocalRemote(t, "acme", "todo")

	tests := []struct {
		name  string
		owner string
		repo  string
		base  string
		host  string
	}{
		{name: "unknown base branch", owner: "acme", repo: "todo", base: "no-such-branch", host: host},
		{name: "unknown repository", owner: "acme", repo: "missing", base: testBase, host: host},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newManager(t, tc.host)
			_, _, err := m.Prepare(context.Background(), tc.owner, tc.repo, tc.base, 1, "t", "", "")
			if err == nil {
				t.Fatal("Prepare() error = nil, want a failure")
			}
		})
	}
}

func TestPrepareRefreshRejectsUnknownBase(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)
	if _, _, err := m.Prepare(ctx, "acme", "todo", testBase, 12, "fix: base", "", "bug"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Prepare(ctx, "acme", "todo", "missing-base", 12, "fix: base", "", "bug"); err == nil {
		t.Fatal("prepared worktree accepted an unknown base")
	}
}

// Every read-only operation must report a clear error on a directory that
// is not a repository, rather than panicking or returning a zero value
// that reads as "no changes".
func TestOperationsOnNonRepository(t *testing.T) {
	ctx := context.Background()
	m := &Manager{WorkDir: t.TempDir()}
	dir := t.TempDir()

	t.Run("CommitAll", func(t *testing.T) {
		if _, err := m.CommitAll(ctx, dir, "msg"); err == nil {
			t.Error("CommitAll() error = nil, want a failure")
		}
	})
	t.Run("ChangedLines", func(t *testing.T) {
		if _, err := m.ChangedLines(ctx, dir, testBase); err == nil {
			t.Error("ChangedLines() error = nil, want a failure")
		}
	})
	t.Run("ChangedFiles", func(t *testing.T) {
		if _, err := m.ChangedFiles(ctx, dir, testBase); err == nil {
			t.Error("ChangedFiles() error = nil, want a failure")
		}
	})
	t.Run("Diff", func(t *testing.T) {
		if _, err := m.Diff(ctx, dir, testBase); err == nil {
			t.Error("Diff() error = nil, want a failure")
		}
	})
	t.Run("Push", func(t *testing.T) {
		if err := m.Push(ctx, dir, "any"); err == nil {
			t.Error("Push() error = nil, want a failure")
		}
	})
}

// The diff must be measured from the merge base, not from the remote tip.
// Otherwise commits landed on the base branch after the task started are
// attributed to the task and can trip the diff-size cap.
func TestDiffUsesMergeBaseNotRemoteTip(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, _, err := m.Prepare(ctx, "acme", "todo", testBase, 5, "feat: mine", "", "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommitAll(ctx, dir, "feat: mine"); err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}

	// Land an unrelated commit on the remote base and fetch it, so
	// origin/main moves ahead of where this branch diverged.
	seed := filepath.Join(t.TempDir(), "other")
	sr, err := git.PlainClone(seed, &git.CloneOptions{URL: filepath.Join(host, "acme", "todo.git")})
	if err != nil {
		t.Fatalf("clone for second author: %v", err)
	}
	writeSeedCommit(t, sr, seed, "theirs.txt", "theirs\n", "chore: theirs")
	ref := plumbing.NewBranchReferenceName(testBase)
	if err := sr.Push(&git.PushOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec(ref + ":" + ref)},
	}); err != nil {
		t.Fatalf("second author push: %v", err)
	}

	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Fetch(&git.FetchOptions{RemoteName: git.DefaultRemoteName}); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	files, err := m.ChangedFiles(ctx, dir, testBase)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	for _, f := range files {
		if f == "theirs.txt" {
			t.Errorf("ChangedFiles() = %v, includes another author's file; diff is not merge-base relative", files)
		}
	}
	if len(files) != 1 || files[0] != "mine.txt" {
		t.Errorf("ChangedFiles() = %v, want [mine.txt]", files)
	}
}

func TestDiffOperationsRejectUnrelatedBaseHistory(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)
	dir, _, err := m.Prepare(ctx, "acme", "todo", testBase, 6, "feat: task", "", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.txt"), []byte("task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := m.CommitAll(ctx, dir, "feat: task"); err != nil || !changed {
		t.Fatalf("CommitAll() = (%v, %v)", changed, err)
	}

	orphanDir := t.TempDir()
	orphan, err := git.PlainInit(orphanDir, false, git.WithDefaultBranch(plumbing.NewBranchReferenceName(testBase)))
	if err != nil {
		t.Fatal(err)
	}
	writeSeedCommit(t, orphan, orphanDir, "orphan.txt", "unrelated\n", "orphan history")
	if _, err := orphan.CreateRemote(&gitconfig.RemoteConfig{Name: git.DefaultRemoteName, URLs: []string{filepath.Join(host, "acme", "todo.git")}}); err != nil {
		t.Fatal(err)
	}
	ref := plumbing.NewBranchReferenceName(testBase)
	if err := orphan.Push(&git.PushOptions{RemoteName: git.DefaultRemoteName, RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec("+" + ref + ":" + ref)}}); err != nil {
		t.Fatal(err)
	}
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Fetch(&git.FetchOptions{RemoteName: git.DefaultRemoteName, Force: true}); err != nil {
		t.Fatal(err)
	}

	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "Diff", run: func() error { _, err := m.Diff(ctx, dir, testBase); return err }},
		{name: "ChangedFiles", run: func() error { _, err := m.ChangedFiles(ctx, dir, testBase); return err }},
		{name: "ChangedLines", run: func() error { _, err := m.ChangedLines(ctx, dir, testBase); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("operation accepted histories without a merge base")
			}
		})
	}
}

func TestBranchNaming(t *testing.T) {
	tests := []struct {
		name   string
		issue  int
		title  string
		labels string
		want   string
	}{
		{name: "bug label maps to fix", issue: 4, title: "crash on save", labels: "bug", want: "fix/4-crash-on-save"},
		{name: "feature label maps to feat", issue: 9, title: "add export", labels: "feature", want: "feat/9-add-export"},
		{name: "no label defaults to feat", issue: 1, title: "tidy", labels: "", want: "feat/1-tidy"},
		{name: "conventional prefix is not duplicated", issue: 2, title: "fix: broken link", labels: "bug", want: "fix/2-broken-link"},
		{name: "empty title falls back to prefix and issue", issue: 8, title: "", labels: "chore", want: "chore/8"},
		{name: "punctuation collapses to single hyphens", issue: 3, title: "a  --  b", labels: "docs", want: "docs/3-a-b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := archieBranch(tc.issue, tc.title, tc.labels); got != tc.want {
				t.Errorf("archieBranch() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCloneURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "explicit forge host",
			baseURL: "https://git.example.test",
			want:    "https://git.example.test/acme/todo.git",
		},
		{
			// The token must never be embedded in the URL: it travels as
			// an in-process credential instead.
			name:    "default github host carries no credentials",
			baseURL: "",
			want:    "https://github.com/acme/todo.git",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{BaseURL: tc.baseURL, BotUser: "archie-bot", Token: "s3cret"}
			got := m.cloneURL("acme", "todo")
			if got != tc.want {
				t.Errorf("cloneURL() = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "s3cret") {
				t.Errorf("cloneURL() leaks the token: %q", got)
			}
		})
	}
}

// A missing forge credential over an HTTP(S) remote must fail with a
// message naming the missing token as the cause, not a bare git transport
// error discovered mid-push. Local (non-HTTP) remotes -- the test double
// used throughout this file -- are unaffected: they never need a token.
func TestPushWithoutTokenOverHTTPFailsWithClearMessage(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	seeder := newManager(t, host)
	dir, branch, err := seeder.Prepare(ctx, "acme", "todo", testBase, 1, "t", "", "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Point the already-cloned worktree's origin at an HTTPS remote, as if
	// it had been cloned against a real forge rather than the local test
	// double, then push with no token configured.
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	if err := r.DeleteRemote(git.DefaultRemoteName); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if _, err := r.CreateRemote(&gitconfig.RemoteConfig{
		Name: git.DefaultRemoteName,
		URLs: []string{"https://forge.example.invalid/acme/todo.git"},
	}); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}

	m := &Manager{WorkDir: seeder.WorkDir, BotUser: "archie-bot"}
	err = m.Push(ctx, dir, branch)
	if err == nil {
		t.Fatal("Push() error = nil, want a failure: no token is configured")
	}
	if !strings.Contains(err.Error(), "forge") && !strings.Contains(err.Error(), "token") {
		t.Errorf("Push() error = %q, want it to name the missing forge credential as the cause", err.Error())
	}
}

func TestAuthOmittedWithoutToken(t *testing.T) {
	if opts := (&Manager{BotUser: "archie-bot"}).auth(); opts != nil {
		t.Errorf("auth() = %v with no token, want nil for an anonymous request", opts)
	}
	if opts := (&Manager{BotUser: "archie-bot", Token: "t"}).auth(); len(opts) == 0 {
		t.Error("auth() returned no options despite a configured token")
	}
}

func TestCleanupRemovesWorktree(t *testing.T) {
	m := &Manager{WorkDir: t.TempDir()}
	dir := m.Dir("acme", "todo", 42)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("acme", "todo", 42); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Cleanup() left the worktree behind")
	}
	// Cleanup of an absent worktree is not an error.
	if err := m.Cleanup("acme", "todo", 42); err != nil {
		t.Errorf("Cleanup() on a missing worktree error = %v, want nil", err)
	}
}

func TestDirDistinguishesRepositoryTuples(t *testing.T) {
	m := &Manager{WorkDir: t.TempDir()}
	first := m.Dir("a-b", "c", 1)
	second := m.Dir("a", "b-c", 1)
	if first == second {
		t.Fatalf("distinct repositories share worktree path %q", first)
	}
}

func TestPrepareMigratesMatchingLegacyWorktree(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)
	legacy := filepath.Join(m.WorkDir, "acme-todo", "issue-44")
	if _, err := git.PlainCloneContext(ctx, legacy, &git.CloneOptions{
		URL:           m.cloneURL("acme", "todo"),
		ReferenceName: plumbing.NewBranchReferenceName(testBase),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, preparedSentinel), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, ".git", "legacy-marker")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	dir, _, err := m.Prepare(ctx, "acme", "todo", testBase, 44, "feat: migrate", "", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if dir != m.Dir("acme", "todo", 44) {
		t.Fatalf("Prepare dir = %q, want %q", dir, m.Dir("acme", "todo", 44))
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "legacy-marker")); err != nil {
		t.Fatalf("legacy worktree was not migrated: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy path still exists after migration: %v", err)
	}
}

func TestPreparePreservesMismatchedLegacyWorktree(t *testing.T) {
	ctx := context.Background()
	desiredHost := newLocalRemote(t, "acme", "todo")
	otherHost := newLocalRemote(t, "other", "widget")
	m := newManager(t, desiredHost)
	legacy := filepath.Join(m.WorkDir, "acme-todo", "issue-45")
	if _, err := git.PlainCloneContext(ctx, legacy, &git.CloneOptions{
		URL:           filepath.Join(otherHost, "other", "widget.git"),
		ReferenceName: plumbing.NewBranchReferenceName(testBase),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, preparedSentinel), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "keep")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.Prepare(ctx, "acme", "todo", testBase, 45, "feat: safe migration", "", "feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("mismatched legacy worktree was altered: %v", err)
	}
}

func TestCleanupHandlesLegacyWorktreesByOrigin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		matching   bool
		wantExists bool
	}{
		{name: "matching origin is removed", matching: true},
		{name: "mismatched origin is preserved", wantExists: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desiredHost := newLocalRemote(t, "acme", "todo")
			m := newManager(t, desiredHost)
			cloneURL := m.cloneURL("acme", "todo")
			if !tc.matching {
				otherHost := newLocalRemote(t, "other", "widget")
				cloneURL = filepath.Join(otherHost, "other", "widget.git")
			}
			legacy := filepath.Join(m.WorkDir, "acme-todo", "issue-46")
			if _, err := git.PlainClone(legacy, &git.CloneOptions{URL: cloneURL}); err != nil {
				t.Fatal(err)
			}
			if err := m.Cleanup("acme", "todo", 46); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(legacy)
			if tc.wantExists && err != nil {
				t.Fatalf("legacy worktree should remain: %v", err)
			}
			if !tc.wantExists && !os.IsNotExist(err) {
				t.Fatalf("legacy worktree still exists: %v", err)
			}
		})
	}
}

// The prepared sentinel must never reach a commit. It previously lived in
// the working tree, hidden by .git/info/exclude -- which git honours and
// go-git does not, so it was committed and pushed onto every task branch.
func TestSentinelIsNeverCommitted(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, _, err := m.Prepare(ctx, "acme", "todo", testBase, 21, "feat: thing", "", "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommitAll(ctx, dir, "feat: thing"); err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}

	files, err := m.ChangedFiles(ctx, dir, testBase)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, "archie-prepared") {
			t.Errorf("the prepared sentinel was committed: ChangedFiles() = %v", files)
		}
	}

	// It must still exist on disk, or Prepare stops being idempotent.
	if _, err := os.Stat(filepath.Join(dir, preparedSentinel)); err != nil {
		t.Errorf("sentinel missing after commit: %v", err)
	}
}

// The daemon writes the container's boot brief into the task worktree before
// the container starts. CommitAll stages with go-git's All option, which does
// not honour .gitignore or .git/info/exclude, so a brief written to the
// worktree root is swept into the agent's commit and pushed onto the task
// branch -- the same failure the prepared sentinel hit. It has to live
// somewhere that can never be tracked.
func TestTaskBriefIsNeverCommitted(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := newManager(t, host)

	dir, _, err := m.Prepare(ctx, "acme", "todo", testBase, 22, "feat: brief", "", "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := container.WriteTaskJSON(dir, container.TaskPayload{
		ID: 22, Owner: "acme", Repo: "todo", Number: 22, Title: "feat: brief",
	}); err != nil {
		t.Fatalf("WriteTaskJSON() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommitAll(ctx, dir, "feat: brief"); err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}

	files, err := m.ChangedFiles(ctx, dir, testBase)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, "task.json") {
			t.Errorf("the task brief was committed: ChangedFiles() = %v", files)
		}
	}

	// The agent still has to be able to read it off its mount.
	if _, err := os.Stat(filepath.Join(dir, ".git", "task.json")); err != nil {
		t.Errorf("brief missing after commit: %v", err)
	}
}
