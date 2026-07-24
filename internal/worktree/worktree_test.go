package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newLocalRemote creates a bare repo with one commit on main and
// returns the directory that serves as the fake forge host: the bare
// repo lives at <host>/<owner>/<repo>.git so cloneURL resolves it via
// file://.
func newLocalRemote(t *testing.T, owner, repo string) string {
	t.Helper()
	host := t.TempDir()
	bare := filepath.Join(host, owner, repo+".git")
	seed := filepath.Join(t.TempDir(), "seed")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	run(host, "init", "--bare", "-b", "main", bare)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	run(seed, "init", "-b", "main")
	run(seed, "config", "user.name", "seeder")
	run(seed, "config", "user.email", "seed@example.com")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(seed, "add", "-A")
	run(seed, "commit", "-m", "seed")
	run(seed, "push", "file://"+bare, "main")
	return host
}

func TestPrepareCommitPushRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	dir, branch, err := m.Prepare(ctx, "acme", "todo", "main", 42, "feat: add test issue title", "", "")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if branch != "feat/42-add-test-issue-title" {
		t.Fatalf("branch = %q", branch)
	}

	// Clean tree commits nothing.
	changed, err := m.CommitAll(ctx, dir, "noop")
	if err != nil || changed {
		t.Fatalf("clean CommitAll = (%v, %v)", changed, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = m.CommitAll(ctx, dir, "add hello")
	if err != nil || !changed {
		t.Fatalf("CommitAll = (%v, %v)", changed, err)
	}

	lines, err := m.ChangedLines(ctx, dir, "main")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}
	if lines != 2 {
		t.Fatalf("ChangedLines = %d, want 2", lines)
	}

	files, err := m.ChangedFiles(ctx, dir, "main")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "hello.txt" {
		t.Fatalf("ChangedFiles = %v, want [hello.txt]", files)
	}

	diff, err := m.Diff(ctx, dir, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "+one") || !strings.Contains(diff, "hello.txt") {
		t.Fatalf("Diff missing expected content: %q", diff)
	}

	if err := m.Push(ctx, dir, branch); err != nil {
		t.Fatalf("push: %v", err)
	}

	// The branch must exist on the remote after push.
	cmd := exec.Command("git", "ls-remote", "--heads",
		"file://"+filepath.Join(host, "acme", "todo.git"), branch)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), branch) {
		t.Fatalf("branch not on remote: %v\n%s", err, out)
	}

	// Committer identity must be the bot.
	cmd = exec.Command("git", "log", "-1", "--format=%an <%ae>")
	cmd.Dir = dir
	out, _ = cmd.CombinedOutput()
	if got := strings.TrimSpace(string(out)); got != "archie-bot <archie-bot@example.com>" {
		t.Fatalf("author = %q", got)
	}

	if err := m.Cleanup("acme", "todo", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup left the worktree behind")
	}
}

func TestPreparePersistentReusesRepoCacheAndCleanupExpiresIt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "todo")
	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}
	const ttl = 24 * time.Hour

	first, _, err := m.PreparePersistent(ctx, "acme", "todo", "main", 41, "feat: first", "", "", ttl)
	if err != nil {
		t.Fatalf("first persistent prepare: %v", err)
	}
	cache := m.cacheDir("acme", "todo")
	if st, err := os.Stat(filepath.Join(cache, "HEAD")); err != nil || st.IsDir() {
		t.Fatalf("repo cache was not created at %s: %v", cache, err)
	}

	second, _, err := m.PreparePersistent(ctx, "acme", "todo", "main", 42, "feat: second", "", "", ttl)
	if err != nil {
		t.Fatalf("second persistent prepare: %v", err)
	}
	if first == second {
		t.Fatalf("persistent tasks share a mutable worktree: %s", first)
	}
	for _, dir := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Fatalf("isolated worktree missing at %s: %v", dir, err)
		}
	}

	expired := time.Now().Add(-ttl - time.Hour)
	if err := os.Chtimes(cache, expired, expired); err != nil {
		t.Fatal(err)
	}
	count, err := m.CleanupExpiredCaches(ttl)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("CleanupExpiredCaches count = %d, want 1", count)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("expired repo cache still exists: %v", err)
	}
	for _, dir := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Fatalf("cache cleanup removed or broke task worktree %s: %v", dir, err)
		}
	}
}

func TestCacheDirCannotEscapeWorkDir(t *testing.T) {
	m := &Manager{WorkDir: t.TempDir()}
	cache := m.cacheDir("../outside", `..\also-outside`)
	rel, err := filepath.Rel(filepath.Join(m.WorkDir, ".repo-cache"), cache)
	if err != nil {
		t.Fatal(err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("cache path escaped managed root: %s", cache)
	}
}

// ---------------------------------------------------------------------------
// Repro tests for uncovered code paths (issue #58)
// ---------------------------------------------------------------------------

// TestPrepareIdempotentFastPath proves the "already prepared" short-circuit
// in prepare(). The second call must return the same dir/branch without
// error, and the worktree must remain functional (fetch+checkout+reset).
func TestPrepareIdempotentFastPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "idem")

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	dir1, branch1, err := m.Prepare(ctx, "acme", "idem", "main", 1, "feat: idempotent", "", "")
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir1, ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	// Second Prepare with the same arguments must hit the fast path.
	dir2, branch2, err := m.Prepare(ctx, "acme", "idem", "main", 1, "feat: idempotent", "", "")
	if err != nil {
		t.Fatalf("second prepare (fast path): %v", err)
	}
	if dir1 != dir2 {
		t.Fatalf("fast path returned different dir: %q vs %q", dir1, dir2)
	}
	if branch1 != branch2 {
		t.Fatalf("fast path returned different branch: %q vs %q", branch1, branch2)
	}

	// The worktree must still be usable after the fast path.
	if _, err := os.Stat(filepath.Join(dir2, ".git")); err != nil {
		t.Fatalf("fast path broke .git: %v", err)
	}

	// After the fast path, we should be able to commit and push.
	if err := os.WriteFile(filepath.Join(dir2, "fast.txt"), []byte("fast path\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := m.CommitAll(ctx, dir2, "test fast path")
	if err != nil {
		t.Fatalf("CommitAll after fast path: %v", err)
	}
	if !changed {
		t.Fatal("CommitAll after fast path reported clean tree, expected changes")
	}
	if err := m.Push(ctx, dir2, branch2); err != nil {
		t.Fatalf("Push after fast path: %v", err)
	}
}

// TestPrepareBadBaseBranch proves that prepare() propagates git clone
// failures when the requested base branch does not exist.
func TestPrepareBadBaseBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "badbase")

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	_, _, err := m.Prepare(ctx, "acme", "badbase", "nonexistent-branch", 1, "feat: test", "", "")
	if err == nil {
		t.Fatal("expected error for nonexistent base branch, got nil")
	}
	// The error must mention the git command ("clone") and the bad branch.
	if !strings.Contains(err.Error(), "clone") {
		t.Fatalf("error should mention 'clone', got: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent-branch") {
		t.Fatalf("error should mention bad branch, got: %v", err)
	}
}

// TestPrepareBadCloneURL proves that prepare() propagates git clone
// failures when the clone URL points to a nonexistent repository.
func TestPrepareBadCloneURL(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file:///nonexistent/path/that/does/not/exist",
	}

	_, _, err := m.Prepare(ctx, "acme", "norepo", "main", 1, "feat: test", "", "")
	if err == nil {
		t.Fatal("expected error for bad clone URL, got nil")
	}
	// The error must mention the git command ("clone").
	if !strings.Contains(err.Error(), "clone") {
		t.Fatalf("error should mention 'clone', got: %v", err)
	}
}

// TestCommitAllInNonGitDir proves that CommitAll returns an error when
// invoked in a directory that is not a git repository.
func TestCommitAllInNonGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused",
		BotUser:  "bot",
		BotEmail: "bot@example.com",
	}

	dir := t.TempDir() // plain directory, no .git
	changed, err := m.CommitAll(ctx, dir, "should fail")
	if err == nil {
		t.Fatalf("expected error for CommitAll in non-git dir, got (changed=%v, err=nil)", changed)
	}
	if changed {
		t.Fatal("CommitAll in non-git dir reported changes, expected false")
	}
}

// TestChangedLinesInNonGitDir proves that ChangedLines returns an error
// when invoked in a directory that is not a git repository.
func TestChangedLinesInNonGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused",
		BotUser:  "bot",
		BotEmail: "bot@example.com",
	}

	dir := t.TempDir() // plain directory, no .git
	lines, err := m.ChangedLines(ctx, dir, "main")
	if err == nil {
		t.Fatalf("expected error for ChangedLines in non-git dir, got (lines=%d, err=nil)", lines)
	}
	if lines != 0 {
		t.Fatalf("ChangedLines in non-git dir returned non-zero lines: %d", lines)
	}
}

// TestDiffInNonGitDir proves that Diff returns an error when invoked
// in a directory that is not a git repository.
func TestDiffInNonGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused",
		BotUser:  "bot",
		BotEmail: "bot@example.com",
	}

	dir := t.TempDir() // plain directory, no .git
	diff, err := m.Diff(ctx, dir, "main")
	if err == nil {
		t.Fatalf("expected error for Diff in non-git dir, got diff=%q, err=nil", diff)
	}
	if diff != "" {
		t.Fatalf("Diff in non-git dir returned non-empty output: %q", diff)
	}
}

// TestPushRejected proves that Push returns an error when the remote
// rejects the push (e.g. non-fast-forward).
func TestPushRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "reject")

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	dir, branch, err := m.Prepare(ctx, "acme", "reject", "main", 1, "feat: push test", "", "")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Push once so the branch exists on the remote.
	if err := os.WriteFile(filepath.Join(dir, "first.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommitAll(ctx, dir, "first commit"); err != nil {
		t.Fatal(err)
	}
	if err := m.Push(ctx, dir, branch); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Now advance the remote branch independently (simulate another
	// client pushing), then try to push again from the stale local branch.
	// We do this by creating a second worktree that pushes to the same
	// branch, making the first worktree's branch out of date.
	dir2, _, err := m.Prepare(ctx, "acme", "reject", "main", 2, "feat: conflicting", "", "")
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	// Checkout the same branch in the second worktree.
	if _, err := exec.Command("git", "-C", dir2, "fetch", "origin", branch).CombinedOutput(); err != nil {
		t.Fatalf("fetch branch in second worktree: %v", err)
	}
	if _, err := exec.Command("git", "-C", dir2, "checkout", "-b", branch, "origin/"+branch).CombinedOutput(); err != nil {
		t.Fatalf("checkout branch in second worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Commit and push from the second worktree using raw git to bypass Manager.
	cmd := exec.Command("git", "-C", dir2, "add", "-A")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add in second worktree: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir2, "commit", "-m", "second commit")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit in second worktree: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir2, "push", "origin", branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("push from second worktree: %v\n%s", err, out)
	}

	// Now the first worktree's branch is behind origin/branch.
	// Make a change and try to push — it should be rejected.
	if err := os.WriteFile(filepath.Join(dir, "third.txt"), []byte("third\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommitAll(ctx, dir, "third commit"); err != nil {
		t.Fatal(err)
	}
	err = m.Push(ctx, dir, branch)
	if err == nil {
		t.Fatal("expected push rejection due to non-fast-forward, got nil error")
	}
}

// TestGitErrorWrapping proves that git() wraps errors with the command
// name and preserves the underlying error via %w.
func TestGitErrorWrapping(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused",
		BotUser:  "bot",
		BotEmail: "bot@example.com",
	}

	dir := t.TempDir() // plain directory, no .git
	_, err := m.CommitAll(ctx, dir, "test message")
	if err == nil {
		t.Fatal("expected error from git in non-repo dir")
	}

	// The error must contain "git" and the specific command ("add").
	errStr := err.Error()
	if !strings.Contains(errStr, "git") {
		t.Fatalf("error should contain 'git' prefix: %v", err)
	}
	if !strings.Contains(errStr, "add") {
		t.Fatalf("error should mention the failing command 'add': %v", err)
	}
}
