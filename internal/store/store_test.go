package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"unicode/utf8"

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

func TestClip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"ascii truncate", "hello", 3, "hel"},
		{"ascii below n", "hello", 10, "hello"},
		{"empty zero", "", 0, ""},
		{"empty positive", "", 5, ""},
		{"2-byte fits exactly", "café", 5, "café"},
		{"2-byte cut off", "café", 4, "caf"},
		{"clean ASCII boundary", "café", 3, "caf"},
		{"4-byte emoji fits", "🍕pizza", 4, "🍕"},
		{"4-byte emoji cut off", "🍕pizza", 3, ""},
		{"2-byte ñ fits", "niño", 5, "niño"},
		{"2-byte ñ cut off", "niño", 4, "niñ"},
		{"3-byte CJK fits", "日本語", 6, "日本"},
		{"3-byte CJK cut off", "日本語", 5, "日"},
		{"mixed multi-byte", "a🍕b", 5, "a🍕"},
		{"mixed multi-byte cut off", "a🍕b", 4, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clip(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("clip(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
			// Verify output is valid UTF-8.
			if !utf8.ValidString(got) {
				t.Errorf("clip(%q, %d) produced invalid UTF-8: %q", tt.input, tt.n, got)
			}
		})
	}
}

func TestClearTerminalTasks(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	statuses := []string{StatusMerged, StatusParked, StatusRejected, StatusClosedWontDo, StatusQueued}
	for i, status := range statuses {
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", i+1, "t", "b", "", ""); err != nil {
			t.Fatal(err)
		}
		task, err := s.TaskByIssue(ctx, "acme", "widget", i+1)
		if err != nil || task == nil {
			t.Fatalf("TaskByIssue(%d) = (%+v, %v)", i+1, task, err)
		}
		if status != StatusQueued {
			if err := s.Transition(ctx, task.ID, StatusQueued, status, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	n, err := s.ClearTerminalTasks(ctx)
	if err != nil || n != 3 {
		t.Fatalf("ClearTerminalTasks = (%d, %v), want (3, nil)", n, err)
	}

	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatusQueued] != 1 {
		t.Fatalf("expected 1 queued, got %d", counts[StatusQueued])
	}
	if counts[StatusParked] != 1 {
		t.Fatalf("recoverable parked task was cleared: count = %d", counts[StatusParked])
	}
	for _, status := range []string{StatusMerged, StatusRejected, StatusClosedWontDo} {
		if counts[status] != 0 {
			t.Fatalf("expected 0 for %s, got %d", status, counts[status])
		}
	}

	// Idempotent: second call removes nothing.
	n, err = s.ClearTerminalTasks(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second ClearTerminalTasks = (%d, %v), want (0, nil)", n, err)
	}
}

func TestArchiveTaskIsGuardedAndScoped(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for number := 1; number <= 2; number++ {
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", number, "t", "b", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	task, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if err := s.Transition(ctx, task.ID, StatusQueued, StatusMerged, "done"); err != nil {
		t.Fatal(err)
	}

	audit := events.Event{Kind: events.KindTaskArchiveRequested, TaskID: task.ID}
	if _, err := s.ArchiveTask(ctx, task.ID, StatusQueued, audit); !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("ArchiveTask stale guard = %v, want ErrStaleTransition", err)
	}
	if got, err := s.TaskByID(ctx, task.ID); err != nil || got == nil {
		t.Fatalf("stale archive removed task: (%+v, %v)", got, err)
	}
	eventID, err := s.ArchiveTask(ctx, task.ID, StatusMerged, audit)
	if err != nil {
		t.Fatalf("ArchiveTask = %v", err)
	}
	if eventID == 0 {
		t.Fatal("ArchiveTask returned no durable event ID")
	}
	if got, err := s.TaskByID(ctx, task.ID); err != nil || got != nil {
		t.Fatalf("archived task = (%+v, %v), want nil", got, err)
	}
	if other, err := s.TaskByIssue(ctx, "acme", "widget", 2); err != nil || other == nil {
		t.Fatalf("archive removed another task: (%+v, %v)", other, err)
	}
}

func TestArchiveAuditFailurePreservesTask(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err := s.Transition(ctx, task.ID, StatusQueued, StatusMerged, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER fail_archive_audit BEFORE INSERT ON events
		WHEN NEW.kind = 'task_archive_requested'
		BEGIN SELECT RAISE(FAIL, 'audit failed'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ArchiveTask(ctx, task.ID, StatusMerged, events.Event{
		Kind: events.KindTaskArchiveRequested, TaskID: task.ID,
	}); err == nil {
		t.Fatal("ArchiveTask succeeded despite forced audit failure")
	}
	if got, err := s.TaskByID(ctx, task.ID); err != nil || got == nil {
		t.Fatalf("audit failure deleted task: (%+v, %v)", got, err)
	}
}

func TestEnqueueIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	ins, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget", "")
	if err != nil || !ins {
		t.Fatalf("first enqueue = (%v, %v)", ins, err)
	}
	ins, err = s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget", "")
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

// TestTransitionRejectsStaleFrom proves that Transition rejects a call
// whose `from` parameter does not match the task's current status.
// Today Transition ignores the `from` guard, so the call succeeds when
// it should fail — this test captures that bug.
func TestTransitionRejectsStaleFrom(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	// Task is now StatusRunning. Transition with from=StatusQueued
	// must fail because the task is not queued.
	err = s.Transition(ctx, task.ID, StatusQueued, StatusPROpen, "stale from")
	if err == nil {
		t.Fatal("Transition with stale 'from' (StatusQueued) on a running task must return an error, but got nil")
	}

	// Verify the task was NOT changed.
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("task status changed to %q despite stale from guard; want %q", got.Status, StatusRunning)
	}
}

// TestTransitionPreventsDoubleTransition proves that two callers racing
// to transition the same task from the same `from` status cannot both
// succeed — the second must get an error because the first already
// changed the status.
func TestTransitionPreventsDoubleTransition(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}

	// First transition: running → pr_open should succeed.
	if err := s.Transition(ctx, task.ID, StatusRunning, StatusPROpen, "first"); err != nil {
		t.Fatalf("first transition = %v", err)
	}

	// Second transition: the task is now StatusPROpen, so
	// from=StatusRunning must fail.
	err = s.Transition(ctx, task.ID, StatusRunning, StatusMerged, "second")
	if err == nil {
		t.Fatal("second Transition with stale 'from' (StatusRunning) on a pr_open task must return an error, but got nil")
	}

	// Verify the task kept the first transition's status.
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != StatusPROpen {
		t.Fatalf("task status = %q, want %q (second transition must not overwrite)", got.Status, StatusPROpen)
	}
}

// TestRequeueRejectsStaleFrom proves that Requeue rejects a call whose
// fromStatus does not match the task's current status.
func TestRequeueRejectsStaleFrom(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	// Task is StatusRunning. Requeue with fromStatus=StatusParked must
	// fail because the task is not parked.
	err = s.Requeue(ctx, task.ID, StatusParked, "implement")
	if err == nil {
		t.Fatal("Requeue with stale fromStatus (StatusParked) on a running task must return an error, but got nil")
	}

	// Verify the task was NOT changed.
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("task status changed to %q despite stale fromStatus guard; want %q", got.Status, StatusRunning)
	}
}

func TestIncrementRetryCount(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}

	if task.RetryCount != 0 {
		t.Fatalf("expected retry_count=0, got %d", task.RetryCount)
	}

	// Increment twice and verify.
	if err := s.IncrementRetryCount(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementRetryCount(ctx, task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", got, err)
	}
	if got.RetryCount != 2 {
		t.Fatalf("expected retry_count=2, got %d", got.RetryCount)
	}
}

func TestRetryTaskAtomicallyRequeuesAndIncrements(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	if err := s.Transition(ctx, task.ID, StatusRunning, StatusParked, "failed"); err != nil {
		t.Fatal(err)
	}

	if err := s.RetryTask(ctx, task.ID, StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != StatusQueued || got.RetryCount != 1 {
		t.Fatalf("task = status %q retry_count %d, want queued/1", got.Status, got.RetryCount)
	}
	if err := s.RetryTask(ctx, task.ID, StatusParked, ""); !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("stale RetryTask error = %v, want ErrStaleTransition", err)
	}
}

func TestRetryTaskWriteFailureLeavesParkedCountUnchanged(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.ClaimNext(ctx)
	if err := s.Transition(ctx, task.ID, StatusRunning, StatusParked, "failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER fail_retry BEFORE UPDATE OF retry_count ON tasks
		BEGIN SELECT RAISE(FAIL, 'retry failed'); END`); err != nil {
		t.Fatal(err)
	}

	if err := s.RetryTask(ctx, task.ID, StatusParked, ""); err == nil {
		t.Fatal("RetryTask succeeded despite forced write failure")
	}
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != StatusParked || got.RetryCount != 0 {
		t.Fatalf("partial retry write: status %q retry_count %d", got.Status, got.RetryCount)
	}
}

func TestRetryCountColumn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}

	// Verify column exists and defaults to 0 via direct query.
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT retry_count FROM tasks WHERE owner='acme' AND repo='todo' AND issue_number=1`).Scan(&count); err != nil {
		t.Fatalf("retry_count scan failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("default retry_count = %d, want 0", count)
	}
}

func TestClaimTransitionAndRecovery(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 7, "t", "", "archie,bug", ""); err != nil {
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

func TestRecoverStalePreservesChatTaskRouting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	created, err := s.EnqueueChatTask(ctx, "acme", "todo", "chat task", "", "tdd", "reviewer", 999_001)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != created.ID {
		t.Fatalf("ClaimNext() = (%+v, %v), want task %d", claimed, err, created.ID)
	}
	if n, err := s.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("RecoverStale() = (%d, %v), want (1, nil)", n, err)
	}
	recovered, err := s.TaskByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil {
		t.Fatal("TaskByID() returned nil after recovery")
	}
	if recovered.Status != StatusQueued || recovered.Source != SourceChat ||
		recovered.Identity != "reviewer" || recovered.Workflow != "tdd" {
		t.Errorf("recovered task = %+v, want queued chat task routed to reviewer/tdd", recovered)
	}
}

func TestLifecycleQueriesPreserveChatTaskRouting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	waiting, err := s.EnqueueChatTask(ctx, "acme", "todo", "needs approval", "", "feasibility", "reviewer", 999_001)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, waiting.ID, StatusQueued, StatusWaitingHuman, "await approval"); err != nil {
		t.Fatal(err)
	}
	// Routing metadata must survive a status transition: a chat task that
	// stops for approval still has to come back to the identity that owns it.
	waitingTask, err := s.TaskByID(ctx, waiting.ID)
	if err != nil || waitingTask == nil {
		t.Fatalf("TaskByID = (%+v, %v)", waitingTask, err)
	}
	if waitingTask.Status != StatusWaitingHuman || waitingTask.Source != SourceChat ||
		waitingTask.Identity != "reviewer" {
		t.Fatalf("waiting task = %+v, want waiting_human chat/reviewer routing", waitingTask)
	}

	pr, err := s.EnqueueChatTask(ctx, "acme", "todo", "open PR", "", "implement", "builder", 999_002)
	if err != nil {
		t.Fatal(err)
	}
	pr.PRNumber = 7
	if err := s.Update(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, pr.ID, StatusQueued, StatusPROpen, "PR #7"); err != nil {
		t.Fatal(err)
	}
	openPRs, err := s.OpenPRs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(openPRs) != 1 || openPRs[0].Source != SourceChat ||
		openPRs[0].Identity != "builder" {
		t.Fatalf("OpenPRs() = %+v, want chat/builder routing", openPRs)
	}
}
