package nell

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

func openTest(t *testing.T) store.TaskStore {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"), "test-node")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}

func TestEnqueueIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	ins, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget")
	if err != nil || !ins {
		t.Fatalf("first enqueue = (%v, %v)", ins, err)
	}
	ins, err = s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget")
	if err != nil || ins {
		t.Fatalf("duplicate enqueue must be no-op, got (%v, %v)", ins, err)
	}
}

func TestClaimNextAndRecovery(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 7, "t", "", "archie,bug"); err != nil {
		t.Fatal(err)
	}

	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	if task.Status != store.StatusRunning || task.Attempt != 1 {
		t.Fatalf("claimed task = %+v", task)
	}

	// Only one queued task, next claim should be nil.
	if next, _ := s.ClaimNext(ctx); next != nil {
		t.Fatal("second claim must return nil while task is running")
	}

	// Recovery.
	if n, err := s.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("recover = (%d, %v)", n, err)
	}
	task, err = s.ClaimNext(ctx)
	if err != nil || task == nil || task.Attempt != 2 {
		t.Fatalf("re-claim after recovery = (%+v, %v)", task, err)
	}
}

func TestTransitionAndUpdate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}

	if err := s.Transition(ctx, task.ID, store.StatusRunning, store.StatusPROpen, "PR #3"); err != nil {
		t.Fatal(err)
	}
	task.PRNumber = 3
	if err := s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}

	open, err := s.OpenPRs(ctx)
	if err != nil || len(open) != 1 || open[0].PRNumber != 3 {
		t.Fatalf("open PRs = (%+v, %v)", open, err)
	}
}

func TestEventRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	id1, err := s.InsertEvent(ctx, events.Event{Kind: "stage_start", TaskID: 1, Stage: "plan"})
	if err != nil || id1 == 0 {
		t.Fatalf("insert 1 = (%d, %v)", id1, err)
	}
	id2, err := s.InsertEvent(ctx, events.Event{
		Kind: "stage_finish", TaskID: 1, Workflow: "implement", Stage: "plan",
		Data: map[string]any{"duration_ms": float64(1200)},
	})
	if err != nil || id2 <= id1 {
		t.Fatalf("insert 2 = (%d, %v)", id2, err)
	}

	evs, err := s.EventsSince(ctx, id1, 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != "stage_finish" {
		t.Fatalf("EventsSince = (%+v, %v)", evs, err)
	}

	timeline, err := s.TaskEvents(ctx, 1)
	if err != nil || len(timeline) != 2 {
		t.Fatalf("TaskEvents = (%d, %v)", len(timeline), err)
	}

	stats, err := s.StageStats(ctx)
	if err != nil || len(stats) != 1 || stats[0].Stage != "plan" {
		t.Fatalf("StageStats = (%+v, %v)", stats, err)
	}
}

func TestStatusCounts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	for i := range 5 {
		if _, err := s.EnqueueIssue(ctx, "acme", "todo", i+1, "t", "", ""); err != nil {
			t.Fatal(err)
		}
	}

	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[store.StatusQueued] != 5 {
		t.Fatalf("expected 5 queued, got %d", counts[store.StatusQueued])
	}
}

func TestClearTerminalTasks(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	statuses := []string{store.StatusMerged, store.StatusParked, store.StatusRejected, store.StatusClosedWontDo, store.StatusQueued}
	for i, status := range statuses {
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", i+1, "t", "b", ""); err != nil {
			t.Fatal(err)
		}
		task, err := s.TaskByIssue(ctx, "acme", "widget", i+1)
		if err != nil || task == nil {
			t.Fatalf("TaskByIssue(%d) = (%+v, %v)", i+1, task, err)
		}
		if status != store.StatusQueued {
			if err := s.Transition(ctx, task.ID, store.StatusQueued, status, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	n, err := s.ClearTerminalTasks(ctx)
	if err != nil || n != 4 {
		t.Fatalf("ClearTerminalTasks = (%d, %v), want (4, nil)", n, err)
	}

	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[store.StatusQueued] != 1 {
		t.Fatalf("expected 1 queued, got %d", counts[store.StatusQueued])
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	ctx := context.Background()

	// Write.
	s1, err := OpenStore(path, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.EnqueueIssue(ctx, "acme", "todo", 1, "survive restart", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify.
	s2, err := OpenStore(path, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	task, err := s2.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("data did not survive restart: (%+v, %v)", task, err)
	}
	if task.Title != "survive restart" {
		t.Fatalf("wrong title: %q", task.Title)
	}
	counts, err := s2.StatusCounts(ctx)
	if err != nil || counts[store.StatusQueued] != 1 {
		t.Fatalf("counts after restart = (%v, %v)", counts, err)
	}
}
