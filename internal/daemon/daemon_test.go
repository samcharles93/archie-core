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

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	arnats "github.com/samcharles93/archie-core/internal/nats"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

type cleanupStorage struct {
	ttl time.Duration
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
		task := &store.Task{Owner: "sam", Repo: "repo-" + string(rune('0'+i))}
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

	dispatcher.Submit(context.Background(), &store.Task{Owner: "sam", Repo: "archie"}, func(context.Context, *store.Task) {
		close(firstStarted)
		<-releaseFirst
	})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "sam", Repo: "archie"}, func(context.Context, *store.Task) {
		close(secondStarted)
	})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "sam", Repo: "tau"}, func(context.Context, *store.Task) {
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
		return owner == "sam" && repo == "archie"
	})
	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})

	dispatcher.Submit(context.Background(), &store.Task{Owner: "sam", Repo: "archie"}, func(context.Context, *store.Task) {
		close(firstStarted)
		<-releaseFirst
	})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "sam", Repo: "archie"}, func(context.Context, *store.Task) {
		close(secondStarted)
	})

	for name, started := range map[string]<-chan struct{}{
		"first opted-in task":  firstStarted,
		"second opted-in task": secondStarted,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s did not start — allow_concurrent repos should not be serialized", name)
		}
	}

	close(releaseFirst)
	dispatcher.Wait()
}

func TestTaskDispatcherWaitsForRunningTasks(t *testing.T) {
	t.Parallel()

	dispatcher := newTaskDispatcher(1, nil)
	release := make(chan struct{})
	dispatcher.Submit(context.Background(), &store.Task{Owner: "sam", Repo: "archie"}, func(context.Context, *store.Task) {
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
				{Owner: "sam", Name: "archie", AllowConcurrent: true},
				{Owner: "sam", Name: "todo"},
			},
		},
	}

	if !d.allowConcurrentFor("sam", "archie") {
		t.Fatal("expected allow_concurrent=true repo to report concurrent-allowed")
	}
	if d.allowConcurrentFor("sam", "todo") {
		t.Fatal("expected repo without allow_concurrent to report false")
	}
	if d.allowConcurrentFor("sam", "unknown") {
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

	got := d.containerEnv()
	for _, want := range []string{
		"NATS_URL=nats://nats.example:4222",
		"NATS_TOKEN=test-nats-token",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("containerEnv() = %q, want %q", got, want)
		}
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

func daemonWithNATS(t *testing.T) (*Daemon, *store.Store) {
	t.Helper()
	srv := startEmbeddedNATSForDaemon(t)
	client, err := arnats.Connect(context.Background(), srv.ClientURL(), "", slog.New(slog.DiscardHandler))
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
		Nats:  client,
		Log:   slog.New(slog.DiscardHandler),
	}
	return d, s
}

func TestRunViaAgentParksOnRequestFailure(t *testing.T) {
	d, s := daemonWithNATS(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "sam", "archie", 1, "t", "b", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	// No responder registered on the taskrun subject — the request must
	// fail, and runViaAgent must park rather than leave the task stuck
	// running.
	d.runViaAgent(ctx, task, config.Repo{Owner: "sam", Name: "archie"})

	got, err := s.TaskByIssue(ctx, "sam", "archie", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status != store.StatusParked {
		t.Fatalf("status = %q, want %q", got.Status, store.StatusParked)
	}
}

func TestRunViaAgentParksOnRunError(t *testing.T) {
	d, s := daemonWithNATS(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "sam", "archie", 2, "t", "b", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	sub, err := d.Nats.Conn().Subscribe(taskrun.SubjectForTask(task.ID), func(msg *natsio.Msg) {
		data, _ := json.Marshal(taskrun.Response{Error: "registry build failed"})
		_ = msg.Respond(data)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { sub.Unsubscribe() })

	d.runViaAgent(ctx, task, config.Repo{Owner: "sam", Name: "archie"})

	got, err := s.TaskByIssue(ctx, "sam", "archie", 2)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue: (%+v, %v)", got, err)
	}
	if got.Status != store.StatusParked {
		t.Fatalf("status = %q, want %q", got.Status, store.StatusParked)
	}
}

func TestRunViaAgentSendsExpectedRequest(t *testing.T) {
	d, s := daemonWithNATS(t)
	d.Cfg.DiffCapLines = 999
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "sam", "archie", 3, "t", "b", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}

	received := make(chan taskrun.Request, 1)
	sub, err := d.Nats.Conn().Subscribe(taskrun.SubjectForTask(task.ID), func(msg *natsio.Msg) {
		var req taskrun.Request
		_ = json.Unmarshal(msg.Data, &req)
		received <- req
		data, _ := json.Marshal(taskrun.Response{Status: store.StatusPROpen})
		_ = msg.Respond(data)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { sub.Unsubscribe() })

	repo := config.Repo{Owner: "sam", Name: "archie", Base: "main"}
	d.runViaAgent(ctx, task, repo)

	select {
	case req := <-received:
		if req.Task == nil || req.Task.ID != task.ID {
			t.Fatalf("request Task = %+v, want ID %d", req.Task, task.ID)
		}
		if req.Repo.FullName() != "sam/archie" {
			t.Fatalf("request Repo = %+v", req.Repo)
		}
		if req.Cfg.DiffCapLines != 999 {
			t.Fatalf("request Cfg.DiffCapLines = %d, want 999", req.Cfg.DiffCapLines)
		}
	case <-time.After(time.Second):
		t.Fatal("archied did not publish a taskrun request")
	}

	// Success path: runViaAgent must not park a task that archie-agent
	// already reported as complete — the daemon just logs it.
	got, err := s.TaskByIssue(ctx, "sam", "archie", 3)
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
func (f *testForge) React(context.Context, string, string, int, string) error { return nil }
func (f *testForge) VerifyPush(context.Context, string, string) error         { return nil }

func testDaemon(t *testing.T, maxRetries int, _ int) (*Daemon, *store.Store, *testForge) {
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

	if _, err := s.EnqueueIssue(ctx, "sam", "todo", 1, "t", "b", ""); err != nil {
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

	task, err = s.TaskByIssue(ctx, "sam", "todo", 1)
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
	repo := config.Repo{Owner: "sam", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	task, err := s.TaskByIssue(ctx, "sam", "todo", 1)
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
	repo := config.Repo{Owner: "sam", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	task, err := s.TaskByIssue(ctx, "sam", "todo", 1)
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
	repo := config.Repo{Owner: "sam", Name: "todo"}

	d.maybeRetryParked(ctx, repo, is)

	if len(fg.stateLabels) != 0 {
		t.Fatalf("expected no state label calls, got %d", len(fg.stateLabels))
	}
	if len(fg.comments) != 0 {
		t.Fatalf("expected no comments, got %d", len(fg.comments))
	}
}

func TestMaybeRetryParkedStillParked(t *testing.T) {
	d, _, fg := testDaemon(t, 3, 0)
	ctx := context.Background()

	is := forge.Issue{Number: 1, Labels: []string{"archie:parked", "bug"}}
	repo := config.Repo{Owner: "sam", Name: "todo"}

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
	repo := config.Repo{Owner: "sam", Name: "todo", MaxRetries: 1}

	d.maybeRetryParked(ctx, repo, is)

	task, err := s.TaskByIssue(ctx, "sam", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if task.Status != store.StatusDead {
		t.Fatalf("expected status dead with repo override, got %s", task.Status)
	}
}
