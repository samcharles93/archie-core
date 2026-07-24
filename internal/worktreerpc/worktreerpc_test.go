package worktreerpc

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/worktree"
)

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func connect(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// newLocalRemote creates a bare repo with one commit on main, reachable
// via file://<host>/<owner>/<repo>.git — mirrors worktree_test.go's helper.
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

func remoteHasBranch(t *testing.T, host, owner, repo, branch string) bool {
	t.Helper()
	bare := filepath.Join(host, owner, repo+".git")
	cmd := exec.Command("git", "ls-remote", "--heads", bare, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls-remote: %v\n%s", err, out)
	}
	return len(out) > 0
}

func TestClientPreparePublishesViaServer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "widget")

	m := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	srv := startEmbedded(t)
	url := srv.ClientURL()

	rpcServer := &Server{Trees: m, Log: slog.New(slog.DiscardHandler)}
	unsub, err := rpcServer.Register(connect(t, url))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(unsub)

	client := &Client{Conn: connect(t, url), Timeout: 5 * time.Second}
	dir, branch, err := client.Prepare(ctx, "acme", "widget", "main", 1, "feat: test", "", "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dir == "" || branch != "feat/1-test" {
		t.Fatalf("Prepare = (%q, %q)", dir, branch)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected worktree cloned at %q: %v", dir, err)
	}
}

func TestClientPushPublishesViaServer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "widget")

	m := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	dir, branch, err := m.Prepare(ctx, "acme", "widget", "main", 1, "feat: test", "", "")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := m.CommitAll(ctx, dir, "add hello"); err != nil || !changed {
		t.Fatalf("CommitAll = (%v, %v)", changed, err)
	}

	srv := startEmbedded(t)
	url := srv.ClientURL()

	rpcServer := &Server{Trees: m, Log: slog.New(slog.DiscardHandler)}
	unsub, err := rpcServer.Register(connect(t, url))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(unsub)

	client := &Client{Conn: connect(t, url), Timeout: 5 * time.Second}
	if err := client.Push(ctx, "acme", "widget", 1, branch); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if !remoteHasBranch(t, host, "acme", "widget", branch) {
		t.Fatalf("branch %q was not pushed to the remote", branch)
	}
}

func TestClientPropagatesServerError(t *testing.T) {
	m := &worktree.Manager{WorkDir: t.TempDir()}
	srv := startEmbedded(t)
	url := srv.ClientURL()

	rpcServer := &Server{Trees: m, Log: slog.New(slog.DiscardHandler)}
	unsub, err := rpcServer.Register(connect(t, url))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(unsub)

	client := &Client{Conn: connect(t, url), Timeout: 5 * time.Second}
	// No worktree ever prepared at this dir — push must fail.
	err = client.Push(context.Background(), "acme", "nonexistent", 999, "some-branch")
	if err == nil {
		t.Fatal("expected Push to propagate the server-side git error")
	}
}

func TestClientPushTimesOutWithNoResponder(t *testing.T) {
	srv := startEmbedded(t)
	client := &Client{Conn: connect(t, srv.ClientURL()), Timeout: 100 * time.Millisecond}

	err := client.Push(context.Background(), "acme", "widget", 1, "branch")
	if err == nil {
		t.Fatal("expected Push to time out with no server registered")
	}
}
