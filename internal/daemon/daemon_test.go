package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/forge"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	arnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/worktree"
)

type cleanupStorage struct {
	ttl time.Duration
}

type identityRunnerStub struct{}

func (*identityRunnerStub) Run(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error) {
	return agentexec.Result{}, nil
}

func (s *cleanupStorage) Setup(context.Context, storage.TaskRef) ([]storage.Mount, error) {
	return nil, nil
}

func (s *cleanupStorage) Teardown(context.Context, storage.TaskRef) error {
	return nil
}

func (s *cleanupStorage) CleanupExpired(_ context.Context, ttl time.Duration) (int, error) {
	s.ttl = ttl
	return 2, nil
}

func TestCleanupExpiredStorageUsesConfiguredTTL(t *testing.T) {
	backend := &cleanupStorage{}
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{
			Containers: config.ContainerConfig{VolumeTTL: config.Duration(6 * time.Hour)},
		}),
		Storage: backend,
		Log:     slog.New(slog.DiscardHandler),
	}

	d.cleanupExpiredStorage(context.Background())

	if backend.ttl != 6*time.Hour {
		t.Fatalf("CleanupExpired ttl = %s, want 6h", backend.ttl)
	}
}

// TestOpenTaskLogOpensAndClosesTheRightSink guards process()'s wiring of
// TaskLogs without the ~10-dependency harness a full process() test would
// need (Store, Forge, Trees, ContainerPool, ...): openTaskLog only ever
// touches d.TaskLogs and d.Log, so it's testable in complete isolation.
// Uses TaskRegistry.Write's own true/false contract as the observable --
// true right after opening (proving Open ran under this task's actual
// ID/attempt, not a typo'd constant), false after the returned closer runs
// (proving it's a real Close, not a no-op).
func TestOpenTaskLogOpensAndClosesTheRightSink(t *testing.T) {
	reg := logging.NewTaskRegistry(t.TempDir(), logging.NewFeed(10), logging.TaskSinkOptions{})
	d := &Daemon{TaskLogs: reg, Log: slog.New(slog.DiscardHandler)}
	task := &store.Task{ID: 5, Attempt: 2}

	closeFn := d.openTaskLog(task)
	if ok := reg.Write(5, logging.Entry{Message: "x"}); !ok {
		t.Fatal("Write() = false right after openTaskLog, want true (sink should be open)")
	}

	closeFn()
	if ok := reg.Write(5, logging.Entry{Message: "y"}); ok {
		t.Error("Write() = true after openTaskLog's closer ran, want false (sink should be closed)")
	}
}

func TestTaskDispatcherEnforcesMaxConcurrency(t *testing.T) {
	t.Parallel()

	const maxConcurrency = 2
	dispatcher := newTaskDispatcher(maxConcurrency, nil)
	release := make(chan struct{})
	started := make(chan int, 3)
	var active atomic.Int32
	var peak atomic.Int32

	for i := 1; i <= 3; i++ {
		task := &store.Task{Owner: "acme", Repo: "repo-" + string(rune('0'+i))}
		dispatcher.Submit(context.Background(), task, func(context.Context, *store.Task) {
			n := active.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			started <- i
			<-release
			active.Add(-1)
		})
	}

	for range maxConcurrency {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("tasks did not start up to max_concurrency")
		}
	}
	select {
	case task := <-started:
		t.Fatalf("task %d started before a concurrency slot was available", task)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	dispatcher.Wait()

	if got := peak.Load(); got != maxConcurrency {
		t.Fatalf("peak concurrency = %d, want %d", got, maxConcurrency)
	}
	select {
	case <-started:
	default:
		t.Fatal("queued task never started after a concurrency slot became available")
	}
}

func TestTaskDispatcherSerializesTasksForSameRepo(t *testing.T) {
	t.Parallel()

	dispatcher := newTaskDispatcher(3, nil)
	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	otherRepoStarted := make(chan struct{})

	dispatcher.Submit(context.Background(), &store.Task{Owner: "acme", Repo: "widget"}, func(context.Context, *store.Task) {
		close(firstStarted)
		<-releaseFirst
	})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "acme", Repo: "widget"}, func(context.Context, *store.Task) {
		close(secondStarted)
	})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "acme", Repo: "gizmo"}, func(context.Context, *store.Task) {
		close(otherRepoStarted)
	})

	for name, started := range map[string]<-chan struct{}{
		"first same-repo task": firstStarted,
		"other-repo task":      otherRepoStarted,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s did not start", name)
		}
	}
	select {
	case <-secondStarted:
		t.Fatal("second task for the same repo ran concurrently")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	dispatcher.Wait()

	select {
	case <-secondStarted:
	default:
		t.Fatal("second same-repo task did not start after the first completed")
	}
}

func TestTaskDispatcherRunsConcurrentReposInParallel(t *testing.T) {
	t.Parallel()

	dispatcher := newTaskDispatcher(3, func(task *store.Task) bool {
		return task.Owner == "acme" && task.Repo == "widget"
	})
	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})

	dispatcher.Submit(context.Background(), &store.Task{Owner: "acme", Repo: "widget"}, func(context.Context, *store.Task) {
		close(firstStarted)
		<-releaseFirst
	})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "acme", Repo: "widget"}, func(context.Context, *store.Task) {
		close(secondStarted)
	})

	for name, started := range map[string]<-chan struct{}{
		"first opted-in task":  firstStarted,
		"second opted-in task": secondStarted,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s did not start  --  allow_concurrent repos should not be serialized", name)
		}
	}

	close(releaseFirst)
	dispatcher.Wait()
}

func TestTaskDispatcherWaitsForRunningTasks(t *testing.T) {
	t.Parallel()

	dispatcher := newTaskDispatcher(1, nil)
	release := make(chan struct{})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "acme", Repo: "widget"}, func(context.Context, *store.Task) {
		<-release
	})

	waited := make(chan struct{})
	go func() {
		dispatcher.Wait()
		close(waited)
	}()

	select {
	case <-waited:
		t.Fatal("Wait returned while a task was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after the running task completed")
	}
}

func TestDaemonAllowConcurrentForReadsRepoConfig(t *testing.T) {
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{
			Repos: []config.Repo{
				{Owner: "acme", Name: "widget", AllowConcurrent: true},
				{Owner: "acme", Name: "todo"},
			},
		}),
	}

	if !d.allowConcurrentForTask(&store.Task{Owner: "acme", Repo: "widget"}) {
		t.Fatal("expected allow_concurrent=true repo to report concurrent-allowed")
	}
	if d.allowConcurrentForTask(&store.Task{Owner: "acme", Repo: "todo"}) {
		t.Fatal("expected repo without allow_concurrent to report false")
	}
	if d.allowConcurrentForTask(&store.Task{Owner: "acme", Repo: "unknown"}) {
		t.Fatal("expected unknown repo to report false")
	}
}

// allowConcurrentForTask must resolve the repo from the identity's own
// repo list when the task names an identity -- root Cfg.Repos must not be
// consulted, or an identity-only repo would silently fall back to the
// serialized default and an overlapping identity repo would use the wrong
// policy.
func TestAllowConcurrentForTaskPrefersOwningIdentityRepo(t *testing.T) {
	shared := config.Repo{Owner: "acme", Name: "shared"}
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{
			// Root (legacy) list: shared is NOT opted in.
			Repos: []config.Repo{shared},
		}),
		Identities: []*IdentityRunner{
			{
				Name: "archie",
				// The identity's own list opts shared in.
				Repos: []config.Repo{{Owner: "acme", Name: "shared", AllowConcurrent: true}},
			},
		},
	}

	if !d.allowConcurrentForTask(&store.Task{Owner: "acme", Repo: "shared", Identity: "archie"}) {
		t.Fatal("identity-owned task must use the identity's repo allow_concurrent")
	}
	// A root-owned (identity-less) task must NOT inherit the identity's
	// opt-in: it resolves through the root repo list.
	if d.allowConcurrentForTask(&store.Task{Owner: "acme", Repo: "shared"}) {
		t.Fatal("identity-less task leaked the identity's allow_concurrent")
	}
}

func TestContainerEnvIncludesConfiguredNATSCredentials(t *testing.T) {
	t.Setenv("ARCHIE_NATS_SECRET", "test-nats-token")

	// ConnectedNATS is the endpoint the daemon's own client connected with
	// at startup; containerEnv deliberately reads this, not the live Cfg,
	// so a reload of nats.url cannot point new containers at a server the
	// daemon is not publishing on.
	natsCfg := config.NATSConfig{
		URL:      "nats://nats.example:4222",
		TokenEnv: "ARCHIE_NATS_SECRET",
	}
	d := &Daemon{
		Cfg:           config.NewHolder(config.Config{}),
		ConnectedNATS: natsCfg,
	}

	got := d.containerEnv(nil)
	for _, want := range []string{
		"NATS_URL=nats://nats.example:4222",
		"NATS_TOKEN=test-nats-token",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("containerEnv() = %q, want %q", got, want)
		}
	}
}

// TestContainerEnvIncludesWorktreeOwnership: the agent process inside a
// container runs as root (no USER in the Dockerfile, no userns-remap), so
// commits it writes to the bind-mounted worktree land owned by UID 0 on the
// host. The daemon then reads those same loose objects to push, running as
// its own non-root host user -- a UID mismatch that surfaces as
// "openat objects/../..: permission denied" (archie-core#520). Passing the
// daemon's own UID/GID lets the agent chown the worktree back before
// pushing.
func TestContainerEnvIncludesWorktreeOwnership(t *testing.T) {
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{}),
	}

	got := d.containerEnv(nil)
	for _, want := range []string{
		fmt.Sprintf("WORKTREE_UID=%d", os.Getuid()),
		fmt.Sprintf("WORKTREE_GID=%d", os.Getgid()),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("containerEnv() = %q, want %q", got, want)
		}
	}
}

func TestContainerEnvUsesOnlyOwningIdentityProviderCredential(t *testing.T) {
	t.Setenv("ROOT_PROVIDER_KEY", "root-secret")
	t.Setenv("WORKER_PROVIDER_KEY", "worker-secret")
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{Providers: map[string]config.Provider{
			"openai": {Class: "openai", APIKeyEnv: "ROOT_PROVIDER_KEY"},
		}}),
		Identities: []*IdentityRunner{{
			Name: "worker",
			Cfg: config.IdentityConfig{Name: "worker", Providers: map[string]config.Provider{
				"openai": {Class: "openai", APIKeyEnv: "WORKER_PROVIDER_KEY"},
			}},
		}},
	}

	got := d.containerEnv(&store.Task{Identity: "worker"})
	if !slices.Contains(got, "WORKER_PROVIDER_KEY=worker-secret") {
		t.Fatalf("identity credential missing from container env: %q", got)
	}
	if slices.Contains(got, "ROOT_PROVIDER_KEY=root-secret") {
		t.Fatalf("root credential leaked into identity container env: %q", got)
	}
}

func startEmbeddedNATSForDaemon(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: t.TempDir()}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	return srv
}

// daemonWithNATS returns the daemon, its store, and the concrete bus
// client -- tests that drive core-NATS subscriptions directly need the
// connection, which the TaskBus contract deliberately does not expose.
func daemonWithNATS(t *testing.T) (*Daemon, *store.Store, *arnats.Client) {
	t.Helper()
	srv := startEmbeddedNATSForDaemon(t)
	client, err := arnats.Connect(context.Background(), arnats.Config{URL: srv.ClientURL(), Subjects: []string{workintake.SubjectTaskWildcard, agentexec.SubjectAgentWildcard}, FilterSubject: workintake.SubjectTaskWildcard}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(client.Close)

	s := store.OpenTest(t)
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{
			Dispatch: config.Dispatch{Labels: map[string]string{
				"parked": "archie:parked", "queued": "archie:queued", "dead": "archie:dead",
			}},
		}),
		Store:          s,
		Tasks:          client,
		WorktreeGrants: fixedGrantIssuer{token: "test-worktree-grant"},
		Log:            slog.New(slog.DiscardHandler),
	}
	return d, s, client
}

type fixedGrantIssuer struct{ token string }

func (g fixedGrantIssuer) Issue(*store.Task) (string, func(), error) {
	return g.token, func() {}, nil
}

func TestRequestTaskRunDoesNotSleepPastReadyDeadline(t *testing.T) {
	d, _, _ := daemonWithNATS(t)
	d.TaskRunReadyTimeout = 10 * time.Millisecond
	d.TaskRunRetryBackoff = time.Second

	start := time.Now()
	_, err := d.requestTaskRun(context.Background(), 1, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("requestTaskRun() error = nil, want no responders")
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("requestTaskRun() took %s, want it to stop at the ready deadline", elapsed)
	}
}

func TestRunViaAgentParksOnRequestFailure(t *testing.T) {
	d, s, _ := daemonWithNATS(t)
	// Bound the no-responders retry window tightly so this test proves
	// "no archie-agent ever showed up" parks the task, without waiting out
	// the real production default (20s).
	d.TaskRunReadyTimeout = 50 * time.Millisecond
	d.TaskRunRetryBackoff = 10 * time.Millisecond
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	// No responder registered on the taskrun subject, ever  --  the retry
	// window must exhaust and runViaAgent must park rather than leave the
	// task stuck running.
	d.runViaAgent(ctx, task, config.Repo{Owner: "acme", Name: "widget"})

	got, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status != store.StatusParked {
		t.Fatalf("status = %q, want %q", got.Status, store.StatusParked)
	}
}

// TestRunViaAgentRetriesUntilResponderAppears is a regression test for the
// deterministic race where a freshly spawned archie-agent container hasn't
// finished connecting to NATS and subscribing yet when the daemon
// publishes its very first taskrun request: ContainerPool.Acquire returns
// as soon as Docker issues the start syscall, well before the container's
// NATS/JetStream setup completes, so the request used to fail with
// nats.ErrNoResponders and park the task on effectively every run. This
// simulates that exact ordering  --  no subscriber at request time, one
// registers shortly after  --  and asserts the retry recovers instead of
// parking.
func TestRunViaAgentRetriesUntilResponderAppears(t *testing.T) {
	d, s, busClient := daemonWithNATS(t)
	d.TaskRunReadyTimeout = time.Second
	d.TaskRunRetryBackoff = 20 * time.Millisecond
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 6, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	// Register the subscriber only after a short delay, simulating the
	// spawned container's NATS/JetStream startup lag  --  the very race
	// this retry loop exists to survive.
	go func() {
		time.Sleep(80 * time.Millisecond)
		coreConn, connErr := busClient.CoreConn()
		if connErr != nil {
			// Fatalf from a non-test goroutine does not stop the test.
			t.Errorf("CoreConn: %v", connErr)
			return
		}
		sub, err := coreConn.Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
			data, _ := json.Marshal(taskrun.Response{Status: store.StatusPROpen})
			_ = msg.Respond(data)
		})
		if err != nil {
			t.Errorf("late subscribe: %v", err)
			return
		}
		t.Cleanup(func() { _ = sub.Unsubscribe() })
	}()

	d.runViaAgent(ctx, task, config.Repo{Owner: "acme", Name: "widget"})

	got, err := s.TaskByIssue(ctx, "acme", "widget", 6)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status == store.StatusParked {
		t.Fatal("runViaAgent parked a task whose responder appeared within the retry window")
	}
}

// TestRunViaAgentDoesNotRetryOnContextCancellation proves requestTaskRun
// doesn't confuse a caller-cancelled context with the "not ready yet"
// no-responders case: it must return promptly (well under the retry
// window) rather than looping through backoff attempts.
func TestRunViaAgentDoesNotRetryOnContextCancellation(t *testing.T) {
	d, s, _ := daemonWithNATS(t)
	d.TaskRunReadyTimeout = 5 * time.Second
	d.TaskRunRetryBackoff = 500 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.EnqueueIssue(context.Background(), "acme", "widget", 7, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(context.Background())
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	start := time.Now()
	d.runViaAgent(ctx, task, config.Repo{Owner: "acme", Name: "widget"})
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("runViaAgent took %s with an already-cancelled context, want it to return immediately without retrying", elapsed)
	}
}

func TestRunViaAgentParksOnRunError(t *testing.T) {
	d, s, busClient := daemonWithNATS(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 2, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
		data, _ := json.Marshal(taskrun.Response{Error: "registry build failed"})
		_ = msg.Respond(data)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	d.runViaAgent(ctx, task, config.Repo{Owner: "acme", Name: "widget"})

	got, err := s.TaskByIssue(ctx, "acme", "widget", 2)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status != store.StatusParked {
		t.Fatalf("status = %q, want %q", got.Status, store.StatusParked)
	}
}

func TestRunViaAgentSendsExpectedRequest(t *testing.T) {
	d, s, busClient := daemonWithNATS(t)
	prev := d.Cfg.Get()
	prev.DiffCapLines = 999
	d.Cfg.Set(prev)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 3, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	received := make(chan taskrun.Request, 1)
	sub, err := mustCoreConn(t, busClient).Subscribe(agentnats.SubjectForTask(task.ID), func(msg *natsio.Msg) {
		var req taskrun.Request
		_ = json.Unmarshal(msg.Data, &req)
		received <- req
		data, _ := json.Marshal(taskrun.Response{Status: store.StatusPROpen})
		_ = msg.Respond(data)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	repo := config.Repo{Owner: "acme", Name: "widget", Base: "main"}
	d.runViaAgent(ctx, task, repo)

	select {
	case req := <-received:
		if req.Task == nil || req.Task.ID != task.ID {
			t.Fatalf("request Task = %+v, want ID %d", req.Task, task.ID)
		}
		if req.Repo.FullName() != "acme/widget" {
			t.Fatalf("request Repo = %+v", req.Repo)
		}
		if req.Cfg.DiffCapLines != 999 {
			t.Fatalf("request Cfg.DiffCapLines = %d, want 999", req.Cfg.DiffCapLines)
		}
		if req.WorktreeGrant != "test-worktree-grant" {
			t.Fatalf("request WorktreeGrant = %q, want dispatch grant", req.WorktreeGrant)
		}
	case <-time.After(time.Second):
		t.Fatal("archied did not publish a taskrun request")
	}

	// Success path: runViaAgent must not park a task that archie-agent
	// already reported as complete  --  the daemon just logs it.
	got, err := s.TaskByIssue(ctx, "acme", "widget", 3)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status == store.StatusParked {
		t.Fatal("runViaAgent parked a task that succeeded")
	}
}

// testForge implements forge.Forge for daemon tests.
type testForge struct {
	comments     []commentCall
	stateLabels  []stateLabelCall
	closedIssues []closeIssueCall
	prStates     map[int]string
}

// pollingForge records every repo the daemon asks about, so a test can
// observe which repos are being polled -- including that a repo removed
// by a reload stops new intake.
type pollingForge struct {
	testForge
	asked []string
}

func (f *pollingForge) AssignedIssues(_ context.Context, owner, repo, _ string) ([]forge.Issue, error) {
	f.asked = append(f.asked, owner+"/"+repo)
	return nil, nil
}

// TestRemovingRepoStopsNewIntake pins the reload behaviour: when a repo
// vanishes from the config, the next poll cycle no longer asks the forge
// about it. In-flight tasks are unaffected because their TaskContext.Cfg
// is a value snapshot taken at dispatch -- the type system guarantees
// that half; this test covers the intake half.
func TestRemovingRepoStopsNewIntake(t *testing.T) {
	fg := &pollingForge{}
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{
			BotUser:  "archie",
			Repos:    []config.Repo{{Owner: "acme", Name: "widget"}},
			Dispatch: config.Dispatch{Trigger: "assignee"},
		}),
		Forge: fg,
		Log:   slog.New(slog.DiscardHandler),
	}

	ctx := context.Background()
	d.poll(ctx)
	if len(fg.asked) != 1 || fg.asked[0] != "acme/widget" {
		t.Fatalf("after first poll, asked = %v, want [acme/widget]", fg.asked)
	}

	// Simulate a reload that drops the repo: the polling loop re-reads
	// d.Cfg.Get().Repos fresh, so the next poll asks about nothing.
	d.Cfg.Set(config.Config{
		BotUser:  "archie",
		Dispatch: config.Dispatch{Trigger: "assignee"},
	})
	d.poll(ctx)
	if len(fg.asked) != 1 {
		t.Fatalf("after reload, asked = %v, want unchanged [acme/widget] (no new intake)", fg.asked)
	}
}

type closeIssueCall struct {
	owner, repo string
	number      int
	comment     string
}

type commentCall struct {
	owner, repo string
	number      int
	body        string
}

type stateLabelCall struct {
	owner, repo string
	number      int
	label       string
	knownLabels []string
}

func (f *testForge) Comment(_ context.Context, owner, repo string, number int, body string) (int64, error) {
	f.comments = append(f.comments, commentCall{owner, repo, number, body})
	return 1, nil
}

func (f *testForge) SetStateLabel(_ context.Context, owner, repo string, number int, label string, knownLabels []string) {
	f.stateLabels = append(f.stateLabels, stateLabelCall{owner, repo, number, label, knownLabels})
}

func (f *testForge) AcceptInvitations(context.Context) error { return nil }
func (f *testForge) AssignedIssues(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (f *testForge) IssuesWithLabel(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (f *testForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected call")
}

func (f *testForge) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	panic("unexpected call")
}

func (f *testForge) PRState(_ context.Context, _, _ string, number int) (string, error) {
	if state, ok := f.prStates[number]; ok {
		return state, nil
	}
	panic("unexpected call")
}

func (f *testForge) CloseIssue(_ context.Context, owner, repo string, number int, comment string) error {
	f.closedIssues = append(f.closedIssues, closeIssueCall{owner, repo, number, comment})
	return nil
}

func (f *testForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	return 0, nil
}
func (f *testForge) React(context.Context, string, string, int, string) error      { return nil }
func (f *testForge) VerifyPush(context.Context, string, string) error              { return nil }
func (f *testForge) LinkBranch(context.Context, string, string, int, string) error { return nil }

func testDaemon(t *testing.T, maxRetries, _ int) (*Daemon, *store.Store, *testForge) {
	t.Helper()

	s := store.OpenTest(t)
	fg := &testForge{}

	cfg := config.Config{
		MaxRetries: maxRetries,
		Dispatch: config.Dispatch{
			Labels: map[string]string{
				"parked": "archie:parked",
				"queued": "archie:queued",
				"dead":   "archie:dead",
			},
		},
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	return &Daemon{
		Cfg:   config.NewHolder(cfg),
		Store: s,
		Forge: fg,
		// Reconcile paths call Trees.Cleanup, which dereferences the
		// manager. A zero Manager is enough: cleanup of a worktree that was
		// never created is a no-op.
		Trees: &worktree.Manager{},
		Log:   log,
	}, s, fg
}

func TestNewIdentityRunnerPopulatesFromConfig(t *testing.T) {
	// Identity names come from config, not hardcoded by the daemon.
	tests := []struct {
		name    string
		idCfg   config.IdentityConfig
		wantErr bool
	}{
		{
			name: "valid identity",
			idCfg: config.IdentityConfig{
				Name:    "my-bot",
				BotUser: "my-bot",
				Forge:   config.Forge{Type: "gitea", Token: secret.SecretRef{Engine: "env", Key: "X"}},
				Repos:   []config.Repo{{Owner: "o", Name: "r"}},
			},
		},
		{
			name: "another identity",
			idCfg: config.IdentityConfig{
				Name:    "other-bot",
				BotUser: "other-bot",
				Forge:   config.Forge{Type: "github", Token: secret.SecretRef{Engine: "env", Key: "Y"}},
				Repos:   []config.Repo{{Owner: "a", Name: "b"}, {Owner: "c", Name: "d"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fg := &testForge{}
			trees := &worktree.Manager{WorkDir: t.TempDir(), Token: "t", BotUser: tt.idCfg.BotUser, BotEmail: "bot@test"}
			ir, err := NewIdentityRunner(ctx, tt.idCfg, fg, trees, slog.New(slog.DiscardHandler))
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewIdentityRunner error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if ir.Name != tt.idCfg.Name {
				t.Errorf("Name = %q, want %q", ir.Name, tt.idCfg.Name)
			}
			if ir.Forge != fg {
				t.Error("Forge not wired")
			}
			if len(ir.Repos) != len(tt.idCfg.Repos) {
				t.Errorf("len(Repos) = %d, want %d", len(ir.Repos), len(tt.idCfg.Repos))
			}
		})
	}
}

func TestNewIdentityRunnerRejectsEmptyName(t *testing.T) {
	ctx := context.Background()
	_, err := NewIdentityRunner(ctx, config.IdentityConfig{Name: ""}, &testForge{}, nil, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestExecutionForUsesIdentityConfigAgentAndProvider(t *testing.T) {
	rootAgent := &identityRunnerStub{}
	identityAgent := &identityRunnerStub{}
	d := &Daemon{
		Cfg: config.NewHolder(config.Config{
			Models: map[string]string{"builder": "root/model"},
			Providers: map[string]config.Provider{
				"root": {Class: "openai", APIKeyEnv: "ROOT_KEY"},
			},
		}),
		Agent: rootAgent,
		Identities: []*IdentityRunner{{
			Name: "worker", Agent: identityAgent,
			Cfg: config.IdentityConfig{
				Name: "worker", Models: map[string]string{"builder": "worker/model"},
				Providers: map[string]config.Provider{
					"worker": {Class: "anthropic", APIKeyEnv: "WORKER_KEY"},
				},
			},
		}},
	}

	cfg, runner := d.executionFor(&store.Task{Identity: "worker"})
	if runner != identityAgent {
		t.Fatal("executionFor returned root agent for identity task")
	}
	if cfg.Models["builder"] != "worker/model" {
		t.Fatalf("builder model = %q", cfg.Models["builder"])
	}
	if _, ok := cfg.Providers["worker"]; !ok {
		t.Fatal("identity provider missing from execution config")
	}
	if _, ok := cfg.Providers["root"]; ok {
		t.Fatal("root provider leaked into identity execution config")
	}
}

func TestDaemonStoresIdentityRunners(t *testing.T) {
	d := &Daemon{
		Identities: []*IdentityRunner{
			{Name: "one"},
			{Name: "two"},
		},
	}
	if len(d.Identities) != 2 {
		t.Errorf("len(Identities) = %d, want 2", len(d.Identities))
	}
}

// ── Multi-identity isolation (archie-core-abg.37) ──────────────────────
//
// Two identities ("archie" and "winter") are both configured to work the
// same owner/repo  --  the exact overlapping-assignment scenario the bead
// exists to make safe. A task enqueued under one identity's name must
// only ever be acted on through that identity's own forge client and
// worktree manager; it must never fall back to the root d.Forge/d.Trees
// or leak into the other identity's client.
func twoIdentityDaemon(t *testing.T) (d *Daemon, s *store.Store, rootFg, archieFg, winterFg *testForge) {
	t.Helper()
	s = store.OpenTest(t)
	rootFg = &testForge{}
	archieFg = &testForge{}
	winterFg = &testForge{}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	repo := config.Repo{Owner: "acme", Name: "shared"}
	d = &Daemon{
		Cfg: config.NewHolder(config.Config{
			Dispatch: config.Dispatch{
				Labels: map[string]string{
					"queued": "archie:queued",
					"dead":   "archie:dead",
				},
			},
			Repos: []config.Repo{repo}, // root/legacy repo list  --  must NOT be consulted for identity tasks
		}),
		Store: s,
		Forge: rootFg, // must NOT be called for identity-owned tasks
		Log:   log,
		Identities: []*IdentityRunner{
			{Name: "archie", Forge: archieFg, Trees: &worktree.Manager{}, Repos: []config.Repo{repo}, Cfg: config.IdentityConfig{Name: "archie"}, Log: log},
			{Name: "winter", Forge: winterFg, Trees: &worktree.Manager{}, Repos: []config.Repo{repo}, Cfg: config.IdentityConfig{Name: "winter"}, Log: log},
		},
	}
	return d, s, rootFg, archieFg, winterFg
}

func TestIdentityForResolvesOwningRunner(t *testing.T) {
	d, _, _, _, _ := twoIdentityDaemon(t)

	if id := d.identityFor(&store.Task{Identity: "archie"}); id == nil || id.Name != "archie" {
		t.Fatalf("identityFor(archie) = %v, want archie runner", id)
	}
	if id := d.identityFor(&store.Task{Identity: "winter"}); id == nil || id.Name != "winter" {
		t.Fatalf("identityFor(winter) = %v, want winter runner", id)
	}
	if id := d.identityFor(&store.Task{Identity: ""}); id != nil {
		t.Fatalf("identityFor(\"\") = %v, want nil (legacy single-identity path)", id)
	}
	if id := d.identityFor(&store.Task{Identity: "nonexistent"}); id != nil {
		t.Fatalf("identityFor(nonexistent) = %v, want nil", id)
	}
}

func TestAcknowledgeUsesOwningIdentityForgeNotRoot(t *testing.T) {
	d, _, rootFg, archieFg, winterFg := twoIdentityDaemon(t)
	ctx := context.Background()
	repo := config.Repo{Owner: "acme", Name: "shared"}
	is := forge.Issue{Number: 1, Title: "shared issue"}

	// archie's poll cycle acknowledges via its own forge client.
	d.acknowledge(ctx, archieFg, d.Cfg.Get(), repo, is)
	if len(archieFg.stateLabels) != 0 {
		t.Fatalf("archie forge got %d state label calls, want 0", len(archieFg.stateLabels))
	}
	if len(winterFg.stateLabels) != 0 || len(rootFg.stateLabels) != 0 {
		t.Fatal("acknowledge leaked a state label call to winter's or root's forge client")
	}
}

func TestRepoForPrefersOwningIdentityRepoList(t *testing.T) {
	d, _, _, _, _ := twoIdentityDaemon(t)

	// Identity-only repo, absent from the root Cfg.Repos list.
	identityOnlyRepo := config.Repo{Owner: "acme", Name: "identity-only"}
	d.Identities[0].Repos = append(d.Identities[0].Repos, identityOnlyRepo)

	got, ok := d.repoFor(&store.Task{Owner: "acme", Repo: "identity-only", Identity: "archie"})
	if !ok {
		t.Fatal("repoFor did not find an identity-only repo via the owning identity's repo list")
	}
	if got.Owner != "acme" || got.Name != "identity-only" {
		t.Fatalf("repoFor = %+v, want sam/identity-only", got)
	}

	// The same repo name must NOT resolve for an unrelated identity.
	if _, ok := d.repoFor(&store.Task{Owner: "acme", Repo: "identity-only", Identity: "winter"}); ok {
		t.Fatal("repoFor leaked archie's identity-only repo to winter")
	}
}

// mustCoreConn returns the raw NATS connection for tests that drive core-NATS
// subscriptions directly, failing the test if the client is not connected.
func mustCoreConn(t *testing.T, c *arnats.Client) *natsio.Conn {
	t.Helper()
	conn, err := c.CoreConn()
	if err != nil {
		t.Fatalf("CoreConn: %v", err)
	}
	return conn
}

// Multi-identity mode must still run the store-wide maintenance passes:
// storage cleanup and PR reconciliation. Before this loop was split into
// per-identity polling plus a single shared maintainAndDrain loop, neither
// ran in multi-identity mode -- merged PRs were never reconciled, tasks
// stayed pr_open forever, and worktrees were never cleaned up.
func TestMaintainAndDrainReconcilesPRsInMultiIdentityMode(t *testing.T) {
	d, s, _, archieFg, _ := twoIdentityDaemon(t)
	ctx := context.Background()

	// A forge-sourced task owned by the "archie" identity whose PR merged.
	if _, err := s.EnqueueIssue(ctx, "acme", "shared", 7, "t", "b", "", "archie"); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	task.PRNumber = 42
	if err := s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, task.ID, store.StatusRunning, store.StatusPROpen, ""); err != nil {
		t.Fatal(err)
	}
	archieFg.prStates = map[int]string{42: "merged"}

	d.maintainAndDrain(ctx)

	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != store.StatusMerged {
		t.Fatalf("status = %q, want %q (multi-identity reconcile never ran)", got.Status, store.StatusMerged)
	}
	if len(archieFg.closedIssues) != 1 {
		t.Fatalf("CloseIssue calls = %d, want 1 (issue must be closed on merge)", len(archieFg.closedIssues))
	}
}

// The identity poll path must use the identity's own dispatch config --
// its bot user, label, and trigger -- not the root daemon's. An identity
// with a different bot user polling "assignee" would otherwise query for
// issues assigned to the root bot user.
func TestPollIssuesWithConfigUsesIdentityScopedDispatch(t *testing.T) {
	fg := &recordingForge{}
	d := &Daemon{Cfg: config.NewHolder(config.Config{}), Log: slog.New(slog.DiscardHandler)}

	// Root uses bot_user "root"; the identity uses "winter". Both default
	// to the assignee trigger.
	rootCfg := config.Config{BotUser: "root", Dispatch: config.Dispatch{Trigger: "assignee"}}
	idCfg := config.IdentityConfig{BotUser: "winter", Dispatch: config.Dispatch{Trigger: "assignee"}}

	// Root poll queries root's bot user.
	_ = d.pollIssuesWithConfig(context.Background(), fg, rootCfg, config.Repo{Owner: "acme", Name: "shared"})
	_ = d.pollIssuesWithConfig(context.Background(), fg, configForIdentity(rootCfg, idCfg), config.Repo{Owner: "acme", Name: "shared"})

	if got := fg.assigned; len(got) != 2 || got[0] != "root" || got[1] != "winter" {
		t.Fatalf("AssignedIssues bot users = %v, want [root winter] (identity dispatch ignored)", got)
	}
}

// An empty cfg.Label with trigger "label" or "either" must never reach
// IssuesWithLabel: GitHub's issues-list API treats an empty label filter as
// "no filter" and returns every open issue in the repo. A live incident
// (GH#445) queued 124 unrelated issues in one poll cycle this way, after
// [dispatch.labels] (a different, unrelated field for task-state labels) was
// configured while the actual trigger-match label was left unset.
func TestPollIssuesWithConfigRefusesEmptyLabel(t *testing.T) {
	tests := []struct {
		name    string
		trigger string
	}{
		{name: "label trigger", trigger: "label"},
		{name: "either trigger", trigger: "either"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fg := &recordingForge{}
			d := &Daemon{Cfg: config.NewHolder(config.Config{}), Log: slog.New(slog.DiscardHandler)}
			cfg := config.Config{Dispatch: config.Dispatch{Trigger: tc.trigger}} // Label left blank.

			issues := d.pollIssuesWithConfig(context.Background(), fg, cfg, config.Repo{Owner: "acme", Name: "shared"})

			if issues != nil {
				t.Fatalf("issues = %v, want nil when label is unset", issues)
			}
			if len(fg.labelled) != 0 {
				t.Fatalf("IssuesWithLabel called with %v, want no call at all (empty label matches every open issue)", fg.labelled)
			}
		})
	}
}

// The "either" trigger must keep polling by assignee when the label is
// empty -- only the label side is unsafe, not the assignee side. A prior
// version of this guard passed even when the empty-label branch dropped
// assignee matches too (return nil instead of return out), because the
// fixture always returned nil from AssignedIssues regardless.
func TestPollIssuesWithConfigEitherTriggerKeepsAssigneeMatchesWhenLabelEmpty(t *testing.T) {
	fg := &recordingForge{assignedIssues: []forge.Issue{{Number: 7}}}
	d := &Daemon{Cfg: config.NewHolder(config.Config{}), Log: slog.New(slog.DiscardHandler)}
	cfg := config.Config{BotUser: "archie", Dispatch: config.Dispatch{Trigger: "either"}} // Label left blank.

	issues := d.pollIssuesWithConfig(context.Background(), fg, cfg, config.Repo{Owner: "acme", Name: "shared"})

	if len(issues) != 1 || issues[0].Number != 7 {
		t.Fatalf("issues = %v, want the one assignee-matched issue preserved", issues)
	}
	if len(fg.labelled) != 0 {
		t.Fatalf("IssuesWithLabel called with %v, want no call at all (empty label matches every open issue)", fg.labelled)
	}
}

// recordingForge records the bot user/label passed to the discovery calls
// so tests can assert identity-scoped polling. Everything else panics.
type recordingForge struct {
	assigned []string
	labelled []string

	// assignedIssues is returned by AssignedIssues, letting tests confirm
	// assignee matches survive alongside (or despite) the label poll.
	assignedIssues []forge.Issue
}

func (f *recordingForge) AssignedIssues(_ context.Context, _, _, botUser string) ([]forge.Issue, error) {
	f.assigned = append(f.assigned, botUser)
	return f.assignedIssues, nil
}

func (f *recordingForge) IssuesWithLabel(_ context.Context, _, _, label string) ([]forge.Issue, error) {
	f.labelled = append(f.labelled, label)
	return nil, nil
}

func (f *recordingForge) Comment(context.Context, string, string, int, string) (int64, error) {
	panic("unexpected call")
}

func (f *recordingForge) SetStateLabel(context.Context, string, string, int, string, []string) {
	panic("unexpected call")
}
func (f *recordingForge) AcceptInvitations(context.Context) error { panic("unexpected call") }
func (f *recordingForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected call")
}

func (f *recordingForge) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	panic("unexpected call")
}

func (f *recordingForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected call")
}

func (f *recordingForge) CloseIssue(context.Context, string, string, int, string) error {
	panic("unexpected call")
}

func (f *recordingForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	panic("unexpected call")
}

func (f *recordingForge) React(context.Context, string, string, int, string) error {
	panic("unexpected call")
}

func (f *recordingForge) VerifyPush(context.Context, string, string) error { panic("unexpected call") }

func (f *recordingForge) LinkBranch(context.Context, string, string, int, string) error {
	panic("unexpected call")
}

// A merged PR must close the forge issue.
//
// "Closes #N" was removed from PR bodies in favour of LinkBranch, which is
// sidebar linkage on Gitea and a no-op stub on GitHub -- neither closes
// anything. So the issue stayed open, labelled and assigned, and the next
// poll re-enqueued it. Once the operator uses the dashboard's Clear, which
// deletes the task row, that is a second implementation and a second PR for
// work already merged.
func TestReconcilePRsClosesTheIssueOnMerge(t *testing.T) {
	tests := []struct {
		name       string
		prState    string
		wantClosed bool
	}{
		{name: "merged closes the issue", prState: "merged", wantClosed: true},
		// A PR closed without merging usually means "try again", so the
		// issue stays open deliberately.
		{name: "closed without merge leaves it open", prState: "closed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, s, fg := testDaemon(t, 3, 0)
			ctx := context.Background()

			if _, err := s.EnqueueIssue(ctx, "acme", "widget", 42, "a task", "", "", ""); err != nil {
				t.Fatal(err)
			}
			task, err := s.ClaimNext(ctx)
			if err != nil || task == nil {
				t.Fatalf("ClaimNext = (%+v, %v)", task, err)
			}
			task.PRNumber = 7
			if err := s.Update(ctx, task); err != nil {
				t.Fatal(err)
			}
			if err := s.Transition(ctx, task.ID, store.StatusRunning, store.StatusPROpen, ""); err != nil {
				t.Fatal(err)
			}
			fg.prStates = map[int]string{7: tc.prState}

			d.reconcilePRs(ctx)

			if tc.wantClosed {
				if len(fg.closedIssues) != 1 {
					t.Fatalf("CloseIssue calls = %d, want 1: the issue stays open "+
						"and is re-polled, so the work is done twice", len(fg.closedIssues))
				}
				got := fg.closedIssues[0]
				if got.owner != "acme" || got.repo != "widget" || got.number != 42 {
					t.Errorf("closed %s/%s#%d, want acme/widget#42", got.owner, got.repo, got.number)
				}
				if got.comment == "" {
					t.Error("closed with no comment")
				}
				return
			}
			if len(fg.closedIssues) != 0 {
				t.Errorf("CloseIssue called %d times for a PR closed without merge",
					len(fg.closedIssues))
			}
		})
	}
}

// A chat task's issue number is synthetic, so closing it would close an
// unrelated issue or error.
func TestReconcilePRsSkipsChatTasks(t *testing.T) {
	d, s, fg := testDaemon(t, 3, 0)
	ctx := context.Background()

	task, err := s.EnqueueChatTask(ctx, "acme", "widget", "chat task", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	task.PRNumber = 8
	if err := s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, task.ID, store.StatusRunning, store.StatusPROpen, ""); err != nil {
		t.Fatal(err)
	}
	fg.prStates = map[int]string{8: "merged"}

	d.reconcilePRs(ctx)

	if len(fg.closedIssues) != 0 {
		t.Errorf("CloseIssue called for a chat task: issue number is synthetic")
	}
}
