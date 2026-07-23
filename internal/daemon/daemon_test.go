package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
)

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

func (f *testForge) AcceptInvitations(context.Context) error                                  { return nil }
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
func (f *testForge) PRState(context.Context, string, string, int) (string, error) { panic("unexpected call") }
func (f *testForge) CloseIssue(context.Context, string, string, int, string) error { panic("unexpected call") }
func (f *testForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) { return 0, nil }
func (f *testForge) React(context.Context, string, string, int, string) error     { return nil }
func (f *testForge) VerifyPush(context.Context, string, string) error             { return nil }

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
