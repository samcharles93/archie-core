package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestTaskDispatcherEnforcesMaxConcurrency(t *testing.T) {
	t.Parallel()

	const maxConcurrency = 2
	dispatcher := newTaskDispatcher(maxConcurrency)
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

	dispatcher := newTaskDispatcher(3)
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

func TestTaskDispatcherWaitsForRunningTasks(t *testing.T) {
	t.Parallel()

	dispatcher := newTaskDispatcher(1)
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
