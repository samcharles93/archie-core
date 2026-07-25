package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

func startEmbeddedForTaskRun(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func connectForTaskRun(t *testing.T, url string) *natsio.Conn {
	t.Helper()
	nc, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// panicRunner fails the test if the bootstrap workflow (deterministic,
// no LLM stages) ever invokes it.
type panicRunner struct{ t *testing.T }

func (r panicRunner) Run(context.Context, string, agentexec.Request) (agentexec.Result, error) {
	r.t.Fatal("bootstrap workflow must not invoke an LLM runner")
	return agentexec.Result{}, nil
}

// prCapturingForge records CreatePR calls and answers the four Forger
// methods the bootstrap workflow needs; everything else panics.
type prCapturingForge struct {
	prs []struct{ owner, repo, title, head, base, body string }
}

func (f *prCapturingForge) Comment(context.Context, string, string, int, string) (int64, error) {
	return 1, nil
}
func (f *prCapturingForge) CloseIssue(context.Context, string, string, int, string) error { return nil }
func (f *prCapturingForge) CreatePR(_ context.Context, owner, repo, title, head, base, body string) (int, error) {
	f.prs = append(f.prs, struct{ owner, repo, title, head, base, body string }{owner, repo, title, head, base, body})
	return 5, nil
}
func (f *prCapturingForge) SetStateLabel(context.Context, string, string, int, string, []string) {}
func (f *prCapturingForge) AcceptInvitations(context.Context) error                              { panic("unexpected call") }
func (f *prCapturingForge) AssignedIssues(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (f *prCapturingForge) IssuesWithLabel(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (f *prCapturingForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected call")
}

func (f *prCapturingForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected call")
}

func (f *prCapturingForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	panic("unexpected call")
}

func (f *prCapturingForge) React(context.Context, string, string, int, string) error {
	panic("unexpected call")
}

func (f *prCapturingForge) VerifyPush(context.Context, string, string) error {
	panic("unexpected call")
}

func (f *prCapturingForge) LinkBranch(context.Context, string, string, int, string) error {
	panic("unexpected call")
}

// newLocalRemote mirrors worktree_test.go's helper: a bare repo with one
// commit on main, reachable via file://<host>/<owner>/<repo>.git.
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

// TestRunTaskExecutesBootstrapWorkflowEndToEnd drives runTask through the
// deterministic "bootstrap" workflow (no LLM calls) with real storerpc/
// forgerpc/worktreerpc servers backing an RPC-constructed TaskContext,
// proving the full archie-agent-side wiring: registry build, Route, Run,
// and the hybrid Trees split between proxied Prepare/Push and local git.
func TestRunTaskExecutesBootstrapWorkflowEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	host := newLocalRemote(t, "acme", "widget")

	// archied side: real Store, a capturing Forge, and the Manager that
	// actually holds the (unused-for-file-remotes) push token.
	st := store.OpenTest(t)
	if _, err := st.EnqueueIssue(ctx, "acme", "widget", 1, "feat: bootstrap test", "", "bootstrap"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	fg := &prCapturingForge{}
	daemonTrees := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}

	// The daemon always prepares the worktree before container acquire  -- 
	// reproduce that here so the container's bind mount has real content.
	hostDir, _, err := daemonTrees.Prepare(ctx, "acme", "widget", "main", 1, task.Title, "", "bootstrap")
	if err != nil {
		t.Fatalf("daemon-side prepare: %v", err)
	}

	srv := startEmbeddedForTaskRun(t)
	url := srv.ClientURL()
	serverConn := connectForTaskRun(t, url)

	unsubStore, err := (&storerpc.Server{Store: st, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatalf("register storerpc: %v", err)
	}
	t.Cleanup(unsubStore)
	unsubForge, err := (&forgerpc.Server{Forge: fg, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatalf("register forgerpc: %v", err)
	}
	t.Cleanup(unsubForge)
	unsubTrees, err := (&worktreerpc.Server{Trees: daemonTrees, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatalf("register worktreerpc: %v", err)
	}
	t.Cleanup(unsubTrees)

	// archie-agent side: its own NATS connection, "bind mount" is just
	// hostDir since this test runs in a single process/filesystem.
	agentConn := connectForTaskRun(t, url)

	req := taskrun.Request{
		Task: task,
		Repo: config.Repo{Owner: "acme", Name: "widget", Base: "main"},
		Cfg:  config.Config{}.ForTask(),
	}

	fakeRunner := agentexec.RunnerFactory(func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner {
		return panicRunner{t}
	})

	resp, err := runTask(ctx, req, agentConn, fakeRunner, hostDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if resp.Status != store.StatusPROpen {
		t.Fatalf("resp.Status = %q, want %q (task: %+v)", resp.Status, store.StatusPROpen, resp.Task)
	}
	if len(fg.prs) != 1 {
		t.Fatalf("expected 1 CreatePR call, got %d: %+v", len(fg.prs), fg.prs)
	}

	// The authoritative state landed in archied's store via storerpc, not
	// just in the in-memory tc.Task.
	stored, err := st.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || stored == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", stored, err)
	}
	if stored.Status != store.StatusPROpen {
		t.Fatalf("stored task status = %q, want %q", stored.Status, store.StatusPROpen)
	}
	if stored.PRNumber != 5 {
		t.Fatalf("stored task PR number = %d, want 5", stored.PRNumber)
	}

	if _, err := os.Stat(filepath.Join(hostDir, ".archie", "bootstrap.md")); err != nil {
		t.Fatalf("expected bootstrap marker file: %v", err)
	}
}
