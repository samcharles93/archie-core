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

func TestAskpassWrittenOnce(t *testing.T) {
	m := &Manager{WorkDir: t.TempDir()}

	p1, err1 := m.askpass()
	if err1 != nil {
		t.Fatalf("first askpass: %v", err1)
	}
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("askpass file not created: %v", err)
	}
	fi1, err := os.Stat(p1)
	if err != nil {
		t.Fatal(err)
	}

	p2, err2 := m.askpass()
	if err2 != nil {
		t.Fatalf("second askpass: %v", err2)
	}
	if p1 != p2 {
		t.Fatalf("paths differ: %q vs %q", p1, p2)
	}

	// Mod time must be unchanged — file was not rewritten.
	fi2, err := os.Stat(p2)
	if err != nil {
		t.Fatal(err)
	}
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatalf("askpass file was rewritten: mod time changed from %v to %v", fi1.ModTime(), fi2.ModTime())
	}

	// Content must be correct.
	b, err := os.ReadFile(p1)
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/sh\necho \"$ARCHIE_GIT_TOKEN\"\n"
	if string(b) != want {
		t.Fatalf("content = %q, want %q", string(b), want)
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
