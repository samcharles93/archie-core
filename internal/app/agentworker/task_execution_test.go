package agentworker

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// ── regression: archie-core#520  --  worktree ownership must be
// reconciled before push, since the agent commits as root inside the
// container and the daemon reads those same loose objects as its own
// non-root host user ──────────────────────────────────────────────────

type fakePushTrees struct {
	pushed  bool
	pushErr error
}

func (f *fakePushTrees) Push(context.Context) error {
	f.pushed = true
	return f.pushErr
}

func TestWorktreeOwnerIDParsesEnvValue(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{"unset", "", -1},
		{"zero is a valid uid (root)", "0", 0},
		{"typical non-root uid", "1000", 1000},
		{"garbage falls back to unset", "not-a-number", -1},
		{"negative falls back to unset", "-1", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := worktreeOwnerID(c.env); got != c.want {
				t.Errorf("worktreeOwnerID(%q) = %d, want %d", c.env, got, c.want)
			}
		})
	}
}

// TestHybridTreesPushReconcilesOwnershipWhenConfigured chowns to the test
// process's own uid/gid -- a permission no-op that succeeds without root --
// so this proves the walk runs and still reaches the underlying push,
// without needing privilege to chown to an arbitrary owner.
func TestHybridTreesPushReconcilesOwnershipWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakePushTrees{}
	trees := &hybridTrees{
		push:        fake,
		localDir:    dir,
		worktreeUID: os.Getuid(),
		worktreeGID: os.Getgid(),
	}

	if err := trees.Push(t.Context(), dir, "branch"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !fake.pushed {
		t.Fatal("Push did not reach the underlying remote push")
	}
}

// TestHybridTreesPushReconcilesOwnershipWithDanglingSymlink: a repo can
// legitimately track a symlink whose target doesn't exist inside the
// container (e.g. a relative link to something outside the checkout, or to
// a tool the image doesn't ship). Chowning must not follow it -- a
// following chown fails on a dangling target, which would abort a push
// that would otherwise have succeeded, a regression worse than the bug
// being fixed here.
func TestHybridTreesPushReconcilesOwnershipWithDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "broken-link")); err != nil {
		t.Fatal(err)
	}

	fake := &fakePushTrees{}
	trees := &hybridTrees{
		push:        fake,
		localDir:    dir,
		worktreeUID: os.Getuid(),
		worktreeGID: os.Getgid(),
	}

	if err := trees.Push(t.Context(), dir, "branch"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !fake.pushed {
		t.Fatal("Push did not reach the underlying remote push")
	}
}

// TestHybridTreesPushSkipsReconciliationWhenNotConfigured: an unset
// WORKTREE_UID/GID (archie-core's non-container and test callers) must not
// attempt any chown at all -- a nonexistent dir would otherwise fail the
// walk, so this proves the walk is skipped entirely, not just tolerant of
// this one input.
func TestHybridTreesPushSkipsReconciliationWhenNotConfigured(t *testing.T) {
	fake := &fakePushTrees{}
	trees := &hybridTrees{
		push:        fake,
		localDir:    filepath.Join(t.TempDir(), "does-not-exist"),
		worktreeUID: -1,
		worktreeGID: -1,
	}

	if err := trees.Push(t.Context(), trees.localDir, "branch"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !fake.pushed {
		t.Fatal("Push did not reach the underlying remote push")
	}
}

func TestHybridTreesCommitReconcilesOwnershipEvenWhenCommitFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	trees := &hybridTrees{
		local:       &worktree.Manager{},
		localDir:    missing,
		worktreeUID: os.Getuid(),
		worktreeGID: os.Getgid(),
	}

	_, err := trees.CommitAll(t.Context(), missing, "test commit")
	if err == nil {
		t.Fatal("CommitAll error = nil, want commit and ownership reconciliation failure")
	}
	if !strings.Contains(err.Error(), "reconcile worktree ownership after commit") {
		t.Fatalf("CommitAll error = %q, want ownership reconciliation context", err)
	}
}

// TestHybridTreesPushSurfacesOwnershipReconciliationFailure: a configured
// but broken reconciliation (the dir push is asked to fix vanished) must
// fail loudly rather than forward to a push that will only fail later with
// a much more confusing git-internal permission error.
func TestHybridTreesPushSurfacesOwnershipReconciliationFailure(t *testing.T) {
	fake := &fakePushTrees{}
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	trees := &hybridTrees{
		push:        fake,
		localDir:    missing,
		worktreeUID: os.Getuid(),
		worktreeGID: os.Getgid(),
	}

	err := trees.Push(t.Context(), missing, "branch")
	if err == nil {
		t.Fatal("expected an error reconciling ownership of a missing directory")
	}
	if fake.pushed {
		t.Fatal("Push must not reach the underlying remote push when reconciliation fails")
	}
}

type panicRunner struct{ t *testing.T }

func (r panicRunner) Run(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
	r.t.Fatal("bootstrap workflow must not invoke an LLM runner")
	return agentexec.Result{}, nil
}

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

func (f *prCapturingForge) LinkBranch(context.Context, string, string, int, string) error { return nil }

type remoteManager struct {
	manager *worktree.Manager
	dir     string
	branch  string
}

func (r *remoteManager) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (string, string, error) {
	dir, branch, err := r.manager.Prepare(ctx, owner, repo, base, issue, title, body, labels)
	if err == nil {
		r.dir = dir
		r.branch = branch
	}
	return dir, branch, err
}

func (r *remoteManager) Push(ctx context.Context) error {
	return r.manager.Push(ctx, r.dir, r.branch)
}

func newLocalRemote(t *testing.T, owner, repo string) string {
	t.Helper()
	host := t.TempDir()
	bare := filepath.Join(host, owner, repo+".git")
	seed := filepath.Join(t.TempDir(), "seed")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
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

func startEmbeddedTaskRPCServer(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return srv
}

func connectTaskRPC(t *testing.T, url string) *natsio.Conn {
	t.Helper()
	connection, err := natsio.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	return connection
}

func TestRunTaskExecutesBootstrapWorkflowEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := t.Context()
	host := newLocalRemote(t, "acme", "widget")
	st := store.OpenTest(t)
	if _, err := st.EnqueueIssue(ctx, "acme", "widget", 1, "feat: bootstrap test", "", "bootstrap", ""); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}
	fg := &prCapturingForge{}
	manager := &worktree.Manager{WorkDir: t.TempDir(), Token: "unused", BotUser: "archie-bot", BotEmail: "archie-bot@example.com", BaseURL: "file://" + host}
	remote := &remoteManager{manager: manager}
	hostDir, _, err := remote.Prepare(ctx, "acme", "widget", "main", 1, task.Title, "", "bootstrap")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	req := taskrun.Request{Task: task, Repo: config.Repo{Owner: "acme", Name: "widget", Base: "main"}, Cfg: config.Config{}.ForTask()}
	fakeRunner := runnerFactory(func(map[string]agentexec.Provider, *slog.Logger) agentexec.Runner { return panicRunner{t} })
	response, err := runTask(ctx, req, taskDependencies{forge: fg, store: st, trees: remote}, fakeRunner, hostDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if response.Status != store.StatusPROpen || len(fg.prs) != 1 {
		t.Fatalf("response status/PRs = (%q, %d), want (%q, 1)", response.Status, len(fg.prs), store.StatusPROpen)
	}
	stored, err := st.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || stored == nil || stored.Status != store.StatusPROpen || stored.PRNumber != 5 {
		t.Fatalf("stored task = (%+v, %v), want PR-open task #5", stored, err)
	}
	if _, err := os.Stat(filepath.Join(hostDir, ".archie", "bootstrap.md")); err != nil {
		t.Fatalf("expected bootstrap marker file: %v", err)
	}
}

// TestExecuteTaskRequestUsesInfrastructureRPCDependencies preserves the
// application-to-infrastructure proof: the application asks Transport for its
// forge/store/tree contracts, and those concrete clients cross embedded NATS
// to daemon-side RPC servers before the bootstrap workflow can complete.
func TestExecuteTaskRequestUsesInfrastructureRPCDependencies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := t.Context()
	host := newLocalRemote(t, "acme", "rpc-widget")
	st := store.OpenTest(t)
	if _, err := st.EnqueueIssue(ctx, "acme", "rpc-widget", 2, "feat: RPC bootstrap test", "", "bootstrap", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%+v, %v)", task, err)
	}
	forge := &prCapturingForge{}
	daemonTrees := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}
	hostDir, branch, err := daemonTrees.Prepare(ctx, "acme", "rpc-widget", "main", 2, task.Title, "", "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	task.Branch = branch
	grants := worktreerpc.NewGrants()
	grant, revokeGrant, err := grants.Issue(task)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revokeGrant)

	srv := startEmbeddedTaskRPCServer(t)
	serverConn := connectTaskRPC(t, srv.ClientURL())
	unsubStore, err := (&storerpc.Server{Store: st, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubStore)
	unsubForge, err := (&forgerpc.Server{Forge: forge, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubForge)
	unsubTrees, err := (&worktreerpc.Server{Trees: daemonTrees, Grants: grants, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubTrees)

	transport, err := agentnats.Connect(ctx, agentnats.Config{URL: srv.ClientURL()}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)

	request := taskrun.Request{
		Task:          task,
		Repo:          config.Repo{Owner: "acme", Name: "rpc-widget", Base: "main"},
		Cfg:           config.Config{}.ForTask(),
		WorktreeGrant: grant,
	}
	response, err := executeTaskRequest(ctx, request, transport, hostDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != store.StatusPROpen || len(forge.prs) != 1 {
		t.Fatalf("response status/PRs = (%q, %d), want (%q, 1)", response.Status, len(forge.prs), store.StatusPROpen)
	}
	// archie-core-1786751948782-3-837a8fd0 point 4: every response reports
	// what this worker actually is, since it's the only channel the daemon
	// has to learn what's really running (see daemon.AgentStatus).
	if response.AgentVersion != Version() {
		t.Errorf("response.AgentVersion = %q, want Version() = %q", response.AgentVersion, Version())
	}
	if response.AgentInstallType != installtype.Type() {
		t.Errorf("response.AgentInstallType = %q, want installtype.Type() = %q", response.AgentInstallType, installtype.Type())
	}
	stored, err := st.TaskByIssue(ctx, "acme", "rpc-widget", 2)
	if err != nil || stored == nil || stored.Status != store.StatusPROpen || stored.PRNumber != 5 {
		t.Fatalf("stored task = (%+v, %v), want RPC-persisted PR-open task #5", stored, err)
	}
	if _, err := os.Stat(filepath.Join(hostDir, ".archie", "bootstrap.md")); err != nil {
		t.Fatalf("expected bootstrap marker file: %v", err)
	}
}

// TestExecuteTaskRequestForwardsWorkflowEventsOverNATS is the regression test
// for the "No events yet" dashboard bug (archie-core-518): a workflow run
// through the NATS/container path publishes its stage/outcome events to an
// in-process *events.Bus nothing on the daemon side could see, because
// runTask never wired TaskContext.Bus. This proves the fix end to end -- a
// bystander subscribed directly to SubjectForEvents(task.ID) on the same
// embedded NATS server executeTaskRequest uses receives real events the
// bootstrap workflow emits, exactly as the daemon's own subscriber would.
func TestExecuteTaskRequestForwardsWorkflowEventsOverNATS(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := t.Context()
	host := newLocalRemote(t, "acme", "events-widget")
	st := store.OpenTest(t)
	if _, err := st.EnqueueIssue(ctx, "acme", "events-widget", 3, "feat: events bootstrap test", "", "bootstrap", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%+v, %v)", task, err)
	}
	forge := &prCapturingForge{}
	daemonTrees := &worktree.Manager{
		WorkDir:  t.TempDir(),
		Token:    "unused-for-file-remotes",
		BotUser:  "archie-bot",
		BotEmail: "archie-bot@example.com",
		BaseURL:  "file://" + host,
	}
	hostDir, branch, err := daemonTrees.Prepare(ctx, "acme", "events-widget", "main", 3, task.Title, "", "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	task.Branch = branch
	grants := worktreerpc.NewGrants()
	grant, revokeGrant, err := grants.Issue(task)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revokeGrant)

	srv := startEmbeddedTaskRPCServer(t)
	serverConn := connectTaskRPC(t, srv.ClientURL())
	unsubStore, err := (&storerpc.Server{Store: st, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubStore)
	unsubForge, err := (&forgerpc.Server{Forge: forge, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubForge)
	unsubTrees, err := (&worktreerpc.Server{Trees: daemonTrees, Grants: grants, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubTrees)

	// Stand in for the daemon's own SubjectEventsWildcard subscriber: bind
	// to the exact per-task subject before the workflow runs, so nothing
	// published during the run can be missed.
	var (
		mu       sync.Mutex
		received []events.Event
	)
	bystander := connectTaskRPC(t, srv.ClientURL())
	eventSub, err := bystander.Subscribe(agentexec.SubjectForEvents(task.ID), func(msg *natsio.Msg) {
		var e events.Event
		if err := json.Unmarshal(msg.Data, &e); err != nil {
			t.Errorf("undecodable event message: %v", err)
			return
		}
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eventSub.Unsubscribe() })
	if err := bystander.Flush(); err != nil {
		t.Fatal(err)
	}

	transport, err := agentnats.Connect(ctx, agentnats.Config{URL: srv.ClientURL()}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)

	request := taskrun.Request{
		Task:          task,
		Repo:          config.Repo{Owner: "acme", Name: "events-widget", Base: "main"},
		Cfg:           config.Config{}.ForTask(),
		WorktreeGrant: grant,
	}
	response, err := executeTaskRequest(ctx, request, transport, hostDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != store.StatusPROpen {
		t.Fatalf("response status = %q, want %q", response.Status, store.StatusPROpen)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		sawOutcome := false
		for _, e := range received {
			if e.Kind == events.KindOutcome {
				sawOutcome = true
				break
			}
		}
		mu.Unlock()
		if sawOutcome {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no \"outcome\" event arrived on SubjectForEvents(task.ID) within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	sawOutcome := false
	for _, e := range received {
		if e.TaskID != task.ID {
			t.Errorf("event %+v carries TaskID %d, want %d", e, e.TaskID, task.ID)
		}
		if e.Kind == events.KindOutcome {
			sawOutcome = true
		}
	}
	if !sawOutcome {
		t.Errorf("received %d events %+v, want at least one %q", len(received), received, events.KindOutcome)
	}
}
