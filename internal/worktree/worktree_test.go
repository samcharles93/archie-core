package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	host := newLocalRemote(t, "sam", "todo")

	m := &Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	dir, branch, err := m.Prepare(ctx, "sam", "todo", "main", 42, "feat: add test issue title", "", "")
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
		"file://"+filepath.Join(host, "sam", "todo.git"), branch)
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

	if err := m.Cleanup("sam", "todo", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup left the worktree behind")
	}
}
