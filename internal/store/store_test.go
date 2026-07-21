package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func TestEnqueueIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	ins, err := s.EnqueueIssue(ctx, "sam", "todo", 1, "add tests", "body", "archie")
	if err != nil || !ins {
		t.Fatalf("first enqueue = (%v, %v)", ins, err)
	}
	ins, err = s.EnqueueIssue(ctx, "sam", "todo", 1, "add tests", "body", "archie")
	if err != nil || ins {
		t.Fatalf("duplicate enqueue must be a no-op, got (%v, %v)", ins, err)
	}
}

func TestEventLogRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	id1, err := s.InsertEvent(ctx, events.Event{Kind: "stage_start", TaskID: 1, Stage: "plan"})
	if err != nil || id1 == 0 {
		t.Fatalf("insert = (%d, %v)", id1, err)
	}
	id2, err := s.InsertEvent(ctx, events.Event{
		Kind: "stage_finish", TaskID: 1, Workflow: "implement", Stage: "plan",
		Data: map[string]any{"duration_ms": 1200},
	})
	if err != nil || id2 <= id1 {
		t.Fatalf("second insert = (%d, %v)", id2, err)
	}

	evs, err := s.EventsSince(ctx, id1, 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != "stage_finish" {
		t.Fatalf("EventsSince = (%+v, %v)", evs, err)
	}
	if evs[0].Data["duration_ms"] != float64(1200) {
		t.Fatalf("data round-trip = %v", evs[0].Data)
	}

	timeline, err := s.TaskEvents(ctx, 1)
	if err != nil || len(timeline) != 2 {
		t.Fatalf("TaskEvents = (%d, %v)", len(timeline), err)
	}

	stats, err := s.StageStats(ctx)
	if err != nil || len(stats) != 1 || stats[0].AvgMs != 1200 || stats[0].Stage != "plan" {
		t.Fatalf("StageStats = (%+v, %v)", stats, err)
	}
}

func TestClaimTransitionAndRecovery(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "sam", "todo", 7, "t", "", "archie,bug"); err != nil {
		t.Fatal(err)
	}

	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	if task.Status != StatusRunning || task.Attempt != 1 || task.Labels != "archie,bug" {
		t.Fatalf("claimed task = %+v", task)
	}
	if next, _ := s.ClaimNext(ctx); next != nil {
		t.Fatal("second claim must return nil while task is running")
	}

	// Crash recovery: running goes back to queued, attempt increments on
	// the next claim.
	if n, err := s.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("recover = (%d, %v)", n, err)
	}
	task, err = s.ClaimNext(ctx)
	if err != nil || task == nil || task.Attempt != 2 {
		t.Fatalf("re-claim after recovery = (%+v, %v)", task, err)
	}

	if err := s.Transition(ctx, task.ID, StatusRunning, StatusPROpen, "PR #3"); err != nil {
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
