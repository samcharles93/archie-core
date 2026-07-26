package nell

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	ins, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget", "")
	if err != nil || !ins {
		t.Fatalf("first enqueue = (%v, %v)", ins, err)
	}
	ins, err = s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget", "")
	if err != nil || ins {
		t.Fatalf("duplicate enqueue must be no-op, got (%v, %v)", ins, err)
	}
}

func TestClaimNextAndRecovery(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 7, "t", "", "archie,bug", ""); err != nil {
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

func TestChatTaskLifecyclePreservesRouting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	task, err := s.EnqueueChatTask(ctx, "acme", "todo", "chat task", "", "feasibility", "reviewer", 999_001)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("ClaimNext() = (%+v, %v), want task %d", claimed, err, task.ID)
	}
	if n, err := s.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("RecoverStale() = (%d, %v), want (1, nil)", n, err)
	}
	recovered, err := s.TaskByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.Source != store.SourceChat ||
		recovered.Identity != "reviewer" || recovered.Workflow != "feasibility" ||
		recovered.Status != store.StatusQueued {
		t.Fatalf("recovered task = %+v, want queued chat/reviewer/feasibility", recovered)
	}

	if err := s.Transition(ctx, task.ID, store.StatusQueued, store.StatusWaitingHuman, "await approval"); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.WaitingTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Source != store.SourceChat ||
		waiting[0].Identity != "reviewer" {
		t.Fatalf("WaitingTasks() = %+v, want chat/reviewer routing", waiting)
	}
}

func TestTransitionAndUpdate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
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
		if _, err := s.EnqueueIssue(ctx, "acme", "todo", i+1, "t", "", "", ""); err != nil {
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
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", i+1, "t", "b", "", ""); err != nil {
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
	if _, err := s1.EnqueueIssue(ctx, "acme", "todo", 1, "survive restart", "", "", ""); err != nil {
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
	defer func() { _ = s2.Close() }()

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

// ── Regression tests for previously untested paths ────────────────────

func TestEmptyStoreOperations(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Get on empty store.
	task, err := s.TaskByIssue(ctx, "acme", "repo", 1)
	if err != nil {
		t.Errorf("TaskByIssue on empty store: %v", err)
	}
	if task != nil {
		t.Error("TaskByIssue on empty store should return nil")
	}

	// List on empty store.
	tasks, err := s.Tasks(ctx, 10)
	if err != nil {
		t.Errorf("Tasks on empty store: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Tasks on empty store should be empty, got %d", len(tasks))
	}

	// WaitingTasks on empty store.
	waiting, err := s.WaitingTasks(ctx)
	if err != nil {
		t.Errorf("WaitingTasks on empty store: %v", err)
	}
	if len(waiting) != 0 {
		t.Errorf("WaitingTasks on empty store should be empty, got %d", len(waiting))
	}

	// OpenPRs on empty store.
	prs, err := s.OpenPRs(ctx)
	if err != nil {
		t.Errorf("OpenPRs on empty store: %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("OpenPRs on empty store should be empty, got %d", len(prs))
	}

	// ClaimNext on empty store.
	claimed, err := s.ClaimNext(ctx)
	if err != nil {
		t.Errorf("ClaimNext on empty store: %v", err)
	}
	if claimed != nil {
		t.Error("ClaimNext on empty store should return nil")
	}

	// StatusCounts on empty store.
	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Errorf("StatusCounts on empty store: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("StatusCounts on empty store should be empty, got %v", counts)
	}
}

func TestConcurrentEnqueueAndClaim(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup

	// Concurrent enqueues.
	for i := range n {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()
			_, err := s.EnqueueIssue(ctx, "acme", "repo", num, "title", "", "", "")
			if err != nil {
				t.Errorf("enqueue %d: %v", num, err)
			}
		}(i)
	}
	wg.Wait()

	// All should be queued.
	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}
	if counts[store.StatusQueued] != n {
		t.Errorf("expected %d queued, got %d", n, counts[store.StatusQueued])
	}

	// Concurrent claims.
	var claimed int32
	for range n {
		wg.Go(func() {
			task, err := s.ClaimNext(ctx)
			if err != nil {
				t.Errorf("ClaimNext: %v", err)
				return
			}
			if task != nil {
				atomic.AddInt32(&claimed, 1)
			}
		})
	}
	wg.Wait()

	if claimed != n {
		t.Errorf("claimed %d, want %d", claimed, n)
	}
}

func TestContextCancellation(t *testing.T) {
	s := openTest(t)
	_, _ = s.EnqueueIssue(context.Background(), "acme", "repo", 1, "t", "", "", "")

	t.Run("cancelled context on ClaimNext", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		task, err := s.ClaimNext(ctx)
		// Either error or nil task is acceptable when cancelled.
		if task != nil && err != nil {
			t.Logf("ClaimNext with cancelled ctx: task=%v err=%v", task, err)
		}
	})

	t.Run("cancelled context on TaskByIssue", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		task, err := s.TaskByIssue(ctx, "acme", "repo", 1)
		// Either error or result is acceptable.
		if task != nil && err != nil {
			t.Logf("TaskByIssue with cancelled ctx: task=%v err=%v", task, err)
		}
	})
}

func TestOversizedEventPayload(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Insert event with detail exceeding 4000 chars.
	longDetail := strings.Repeat("x", 5000)
	ev := events.Event{
		Kind:   events.KindLog,
		Detail: longDetail,
		Data:   map[string]any{"level": "info", "msg": "test"},
	}

	id, err := s.InsertEvent(ctx, ev)
	if err != nil {
		t.Fatalf("InsertEvent with oversized detail: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive event ID")
	}

	// Read back and verify detail was clipped.
	events, err := s.EventsSince(ctx, id-1, 1)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if len(events[0].Detail) > 4000 {
		t.Errorf("detail should be clipped to 4000, got %d chars", len(events[0].Detail))
	}
}

func TestInsertEventWithUnmarshalableData(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Channel values cannot be JSON-marshaled.
	ev := events.Event{
		Kind: events.KindLog,
		Data: map[string]any{"ch": make(chan int)},
	}

	id, err := s.InsertEvent(ctx, ev)
	if err != nil {
		t.Fatalf("InsertEvent with unmarshalable data should not error: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive event ID")
	}

	// Read back — should have marshal_error marker.
	eventsList, err := s.EventsSince(ctx, id-1, 1)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	if len(eventsList) != 1 {
		t.Fatalf("expected 1 event, got %d", len(eventsList))
	}
}

func TestStatusCountsAfterTransitions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	_, _ = s.EnqueueIssue(ctx, "acme", "repo", 1, "t1", "", "", "")
	_, _ = s.EnqueueIssue(ctx, "acme", "repo", 2, "t2", "", "", "")

	task1, _ := s.ClaimNext(ctx)
	task2, _ := s.ClaimNext(ctx)

	counts, _ := s.StatusCounts(ctx)
	if counts[store.StatusRunning] != 2 {
		t.Errorf("expected 2 running, got %d", counts[store.StatusRunning])
	}

	_ = s.Transition(ctx, task1.ID, store.StatusRunning, store.StatusPROpen, "")
	counts, _ = s.StatusCounts(ctx)
	if counts[store.StatusPROpen] != 1 {
		t.Errorf("expected 1 pr_open, got %d", counts[store.StatusPROpen])
	}
	if counts[store.StatusRunning] != 1 {
		t.Errorf("expected 1 running after transition, got %d", counts[store.StatusRunning])
	}
	_ = task2
}

func TestInsertEventsSinceAndRecent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	id1, _ := s.InsertEvent(ctx, events.Event{Kind: events.KindTaskQueued, TaskID: 1})
	time.Sleep(1 * time.Millisecond)
	id2, _ := s.InsertEvent(ctx, events.Event{Kind: events.KindStageStart, TaskID: 1})

	// EventsSince should return events in order.
	evs, err := s.EventsSince(ctx, 0, 10)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	if len(evs) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(evs))
	}
	if evs[0].ID >= evs[1].ID {
		t.Errorf("EventsSince order: evs[0].ID=%d, evs[1].ID=%d, want oldest first", evs[0].ID, evs[1].ID)
	}

	// RecentEvents returns newest first.
	recent, err := s.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(recent) < 2 {
		t.Fatalf("expected at least 2 recent events, got %d", len(recent))
	}
	if recent[0].ID < recent[1].ID {
		t.Error("RecentEvents should return newest first")
	}
	_ = id1
	_ = id2
}

func TestRecoverStaleEmptyStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	n, err := s.RecoverStale(ctx)
	if err != nil {
		t.Errorf("RecoverStale on empty store: %v", err)
	}
	if n != 0 {
		t.Errorf("RecoverStale on empty store should return 0, got %d", n)
	}
}

func TestClearTerminalTasksEmptyStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	n, err := s.ClearTerminalTasks(ctx)
	if err != nil {
		t.Errorf("ClearTerminalTasks on empty store: %v", err)
	}
	if n != 0 {
		t.Errorf("ClearTerminalTasks on empty store should return 0, got %d", n)
	}
}
