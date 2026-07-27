package worktreerpc

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
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

// newLocalRemote creates a bare repo with one commit on main at
// <host>/<owner>/<repo>.git. Built with go-git, so this package's tests
// no longer require a git binary on the machine running them.
func newLocalRemote(t *testing.T, owner, repo string) string {
	t.Helper()
	host := t.TempDir()
	bare := filepath.Join(host, owner, repo+".git")
	mainRef := plumbing.NewBranchReferenceName("main")

	if _, err := git.PlainInit(bare, true, git.WithDefaultBranch(mainRef)); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	seed := filepath.Join(t.TempDir(), "seed")
	sr, err := git.PlainInit(seed, false, git.WithDefaultBranch(mainRef))
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	// Fixtures must not inherit commit.gpgSign from the host's global
	// git config: go-git cannot sign and would fail the commit.
	cfg, err := sr.Config()
	if err != nil {
		t.Fatalf("read seed config: %v", err)
	}
	cfg.Raw.Section("commit").SetOption("gpgsign", "false")
	if err := sr.SetConfig(cfg); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt, err := sr.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	sig := &object.Signature{
		Name:  "seeder",
		Email: "seed@example.com",
		When:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := wt.Commit("seed", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if _, err := sr.CreateRemote(&gitconfig.RemoteConfig{
		Name: git.DefaultRemoteName,
		URLs: []string{bare},
	}); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	if err := sr.Push(&git.PushOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec(mainRef + ":" + mainRef)},
	}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	return host
}

func remoteHasBranch(t *testing.T, host, owner, repo, branch string) bool {
	t.Helper()
	bare := filepath.Join(host, owner, repo+".git")
	r, err := git.PlainOpen(bare)
	if err != nil {
		t.Fatalf("open bare remote: %v", err)
	}
	_, err = r.Reference(plumbing.NewBranchReferenceName(branch), true)
	return err == nil
}

func TestClientPreparePublishesViaServer(t *testing.T) {
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "widget")

	m := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  host,
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
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "widget")

	m := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  host,
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
	// No worktree ever prepared at this dir  --  push must fail.
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

// A handler must impose its own deadline. NATS request/reply carries no
// deadline to the server, and a clone or push now runs in-process via
// go-git, so an unresponsive forge would otherwise wedge the handler
// goroutine forever.
func TestServerHandlerContextIsBounded(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		wantMax time.Duration
	}{
		{name: "explicit timeout is honoured", timeout: 42 * time.Second, wantMax: 42 * time.Second},
		{name: "zero falls back to the default", timeout: 0, wantMax: defaultHandlerTimeout},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{Timeout: tc.timeout}
			ctx, cancel := s.handlerContext()
			defer cancel()

			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("handlerContext() returned a context with no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > tc.wantMax {
				t.Errorf("deadline in %v, want a positive value no greater than %v", remaining, tc.wantMax)
			}
		})
	}
}
