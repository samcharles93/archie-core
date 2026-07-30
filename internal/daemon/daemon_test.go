package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
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
	arnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
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

func (*identityRunnerStub) Run(context.Context, string, agentexec.Request) (agentexec.Result, error) {
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
		Cfg: config.Config{
			Containers: config.ContainerConfig{VolumeTTL: config.Duration(6 * time.Hour)},
		},
		Storage: backend,
		Log:     slog.New(slog.DiscardHandler),
	}

	d.cleanupExpiredStorage(context.Background())

	if backend.ttl != 6*time.Hour {
		t.Fatalf("CleanupExpired ttl = %s, want 6h", backend.ttl)
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

	dispatcher := newTaskDispatcher(3, func(owner, repo string) bool {
		return owner == "acme" && repo == "widget"
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
		Cfg: config.Config{
			Repos: []config.Repo{
				{Owner: "acme", Name: "widget", AllowConcurrent: true},
				{Owner: "acme", Name: "todo"},
			},
		},
	}

	if !d.allowConcurrentFor("acme", "widget") {
		t.Fatal("expected allow_concurrent=true repo to report concurrent-allowed")
	}
	if d.allowConcurrentFor("acme", "todo") {
		t.Fatal("expected repo without allow_concurrent to report false")
	}
	if d.allowConcurrentFor("acme", "unknown") {
		t.Fatal("expected unknown repo to report false")
	}
}

func TestContainerEnvIncludesConfiguredNATSCredentials(t *testing.T) {
	t.Setenv("ARCHIE_NATS_SECRET", "test-nats-token")

	d := &Daemon{Cfg: config.Config{
		NATS: config.NATSConfig{
			URL:      "nats://nats.example:4222",
			TokenEnv: "ARCHIE_NATS_SECRET",
		},
	}}

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

func TestContainerEnvUsesOnlyOwningIdentityProviderCredential(t *testing.T) {
	t.Setenv("ROOT_PROVIDER_KEY", "root-secret")
	t.Setenv("WORKER_PROVIDER_KEY", "worker-secret")
	d := &Daemon{
		Cfg: config.Config{Providers: map[string]config.Provider{
			"openai": {Class: "openai", APIKeyEnv: "ROOT_PROVIDER_KEY"},
		}},
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
		Cfg: config.Config{
			Dispatch: config.Dispatch{Labels: map[string]string{
				"parked": "archie:parked", "queued": "archie:queued", "dead": "archie:dead",
			}},
		},
		Store: s,
		Tasks: client,
		Log:   slog.New(slog.DiscardHandler),
	}
	return d, s, client
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
		sub, err := coreConn.Subscribe(taskrun.SubjectForTask(task.ID), func(msg *natsio.Msg) {
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

	sub, err := mustCoreConn(t, busClient).Subscribe(taskrun.SubjectForTask(task.ID), func(msg *natsio.Msg) {
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
	d.Cfg.DiffCapLines = 999
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 3, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	received := make(chan taskrun.Request, 1)
	sub, err := mustCoreConn(t, busClient).Subscribe(taskrun.SubjectForTask(task.ID), func(msg *natsio.Msg) {
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

func TestHasLabelUsesExactNames(t *testing.T) {
	labels := []string{"bug", "archie:parked-old", "feature", "custom,label"}
	if hasLabel(labels, "archie:parked") {
		t.Fatal("hasLabel matched a label-name substring")
	}
	if !hasLabel(labels, "custom,label") {
		t.Fatal("hasLabel did not preserve a label containing a comma")
	}
	labels = append(labels, "archie:parked")
	if !hasLabel(labels, "archie:parked") {
		t.Fatal("hasLabel did not match the exact label name")
	}
}

// testForge implements forge.Forge for daemon tests.
type testForge struct {
	comments    []commentCall
	stateLabels []stateLabelCall
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

func (f *testForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected call")
}

func (f *testForge) CloseIssue(context.Context, string, string, int, string) error {
	panic("unexpected call")
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
		Cfg:   cfg,
		Store: s,
		Forge: fg,
		Log:   log,
	}, s, fg
}

func setupParkedTask(t *testing.T, s *store.Store, retryCount int) *store.Task {
	t.Helper()
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	if err := s.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "parked"); err != nil {
		t.Fatal(err)
	}
	for range retryCount {
		if err := s.IncrementRetryCount(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}

	task, err = s.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	return task
}

func TestMaybeRetryParkedUnderThreshold(t *testing.T) {
	d, s, fg := testDaemon(t, 3, 0)
	ctx := context.Background()

	_ = setupParkedTask(t, s, 0)

	is := forge.Issue{Number: 1, Labels: []string{}}
	repo := config.Repo{Owner: "acme", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	task, err := s.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if task.Status != store.StatusQueued {
		t.Fatalf("expected status queued, got %s", task.Status)
	}
	if task.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got %d", task.RetryCount)
	}
	if len(fg.stateLabels) != 1 || fg.stateLabels[0].label != "archie:queued" {
		t.Fatalf("expected queued state label, got %+v", fg.stateLabels)
	}
	if len(fg.comments) != 0 {
		t.Fatalf("expected no comments, got %d", len(fg.comments))
	}
}

func TestMaybeRetryParkedAtThreshold(t *testing.T) {
	d, s, fg := testDaemon(t, 3, 0)
	ctx := context.Background()

	_ = setupParkedTask(t, s, 3)

	is := forge.Issue{Number: 1, Labels: []string{}}
	repo := config.Repo{Owner: "acme", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	task, err := s.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if task.Status != store.StatusDead {
		t.Fatalf("expected status dead, got %s", task.Status)
	}
	if len(fg.stateLabels) != 1 || fg.stateLabels[0].label != "archie:dead" {
		t.Fatalf("expected dead state label, got %+v", fg.stateLabels)
	}
	if len(fg.comments) != 1 || !strings.Contains(fg.comments[0].body, "Max retries reached") {
		t.Fatalf("expected dead comment, got %+v", fg.comments)
	}
}

func TestMaybeRetryParkedSkipsDead(t *testing.T) {
	d, s, fg := testDaemon(t, 3, 0)
	ctx := context.Background()

	task := setupParkedTask(t, s, 0)
	if err := s.Transition(ctx, task.ID, store.StatusParked, store.StatusDead, "manual"); err != nil {
		t.Fatal(err)
	}

	is := forge.Issue{Number: 1, Labels: []string{}}
	repo := config.Repo{Owner: "acme", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	if len(fg.stateLabels) != 0 {
		t.Fatalf("expected no state label calls, got %d", len(fg.stateLabels))
	}
	if len(fg.comments) != 0 {
		t.Fatalf("expected no comments, got %d", len(fg.comments))
	}
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
		Cfg: config.Config{
			Models: map[string]string{"builder": "root/model"},
			Providers: map[string]config.Provider{
				"root": {Class: "openai", APIKeyEnv: "ROOT_KEY"},
			},
		},
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

func TestMaybeRetryParkedStillParked(t *testing.T) {
	d, _, fg := testDaemon(t, 3, 0)
	ctx := context.Background()

	is := forge.Issue{Number: 1, Labels: []string{"archie:parked", "bug"}}
	repo := config.Repo{Owner: "acme", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	if len(fg.stateLabels) != 0 {
		t.Fatalf("expected no state label calls, got %d", len(fg.stateLabels))
	}
}

func TestMaybeRetryParkedRepoOverride(t *testing.T) {
	d, s, _ := testDaemon(t, 3, 1)
	ctx := context.Background()

	_ = setupParkedTask(t, s, 1)

	is := forge.Issue{Number: 1, Labels: []string{}}
	repo := config.Repo{Owner: "acme", Name: "todo", MaxRetries: 1}

	d.maybeRetryParked(ctx, repo, is)

	task, err := s.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if task.Status != store.StatusDead {
		t.Fatalf("expected status dead with repo override, got %s", task.Status)
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
		Cfg: config.Config{
			Dispatch: config.Dispatch{
				Labels: map[string]string{
					"queued": "archie:queued",
					"dead":   "archie:dead",
				},
			},
			Repos: []config.Repo{repo}, // root/legacy repo list  --  must NOT be consulted for identity tasks
		},
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
	d.acknowledge(ctx, archieFg, repo, is)
	if len(archieFg.stateLabels) != 1 {
		t.Fatalf("archie forge got %d state label calls, want 1", len(archieFg.stateLabels))
	}
	if len(winterFg.stateLabels) != 0 || len(rootFg.stateLabels) != 0 {
		t.Fatal("acknowledge leaked a state label call to winter's or root's forge client")
	}
}

func TestMarkDeadResolvesForgeFromTaskIdentityNotCaller(t *testing.T) {
	d, s, rootFg, archieFg, winterFg := twoIdentityDaemon(t)
	ctx := context.Background()

	// Two tasks for the SAME owner/repo, owned by different identities.
	if _, err := s.EnqueueIssue(ctx, "acme", "shared", 1, "t1", "b", "", "archie"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueIssue(ctx, "acme", "shared", 2, "t2", "b", "", "winter"); err != nil {
		t.Fatal(err)
	}
	archieTask, err := s.TaskByIssue(ctx, "acme", "shared", 1)
	if err != nil || archieTask == nil {
		t.Fatalf("TaskByIssue(1) = (%+v, %v)", archieTask, err)
	}
	winterTask, err := s.TaskByIssue(ctx, "acme", "shared", 2)
	if err != nil || winterTask == nil {
		t.Fatalf("TaskByIssue(2) = (%+v, %v)", winterTask, err)
	}
	if err := s.Transition(ctx, archieTask.ID, store.StatusQueued, store.StatusParked, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, winterTask.ID, store.StatusQueued, store.StatusParked, "test"); err != nil {
		t.Fatal(err)
	}
	archieTask, _ = s.TaskByIssue(ctx, "acme", "shared", 1)
	winterTask, _ = s.TaskByIssue(ctx, "acme", "shared", 2)

	repo := config.Repo{Owner: "acme", Name: "shared"}
	d.markDead(ctx, repo, archieTask, 0)
	d.markDead(ctx, repo, winterTask, 0)

	if len(archieFg.stateLabels) != 1 || archieFg.stateLabels[0].number != 1 {
		t.Fatalf("archie forge state labels = %+v, want exactly issue 1", archieFg.stateLabels)
	}
	if len(winterFg.stateLabels) != 1 || winterFg.stateLabels[0].number != 2 {
		t.Fatalf("winter forge state labels = %+v, want exactly issue 2", winterFg.stateLabels)
	}
	if len(rootFg.stateLabels) != 0 {
		t.Fatal("markDead used the root forge client instead of the owning identity's")
	}
	if len(archieFg.comments) != 1 || len(winterFg.comments) != 1 {
		t.Fatalf("expected one dead-comment per identity, got archie=%d winter=%d", len(archieFg.comments), len(winterFg.comments))
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
