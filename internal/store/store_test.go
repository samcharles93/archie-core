package store

import (
	"context"
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
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", i+1, "t", "b", ""); err != nil {
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
	if err != nil || n != 4 {
		t.Fatalf("ClearTerminalTasks = (%d, %v), want (4, nil)", n, err)
	}

	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatusQueued] != 1 {
		t.Fatalf("expected 1 queued, got %d", counts[StatusQueued])
	}
	for _, status := range []string{StatusMerged, StatusParked, StatusRejected, StatusClosedWontDo} {
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

func TestEnqueueIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	ins, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget")
	if err != nil || !ins {
		t.Fatalf("first enqueue = (%v, %v)", ins, err)
	}
	ins, err = s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget")
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

func TestIncrementRetryCount(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", ""); err != nil {
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

func TestRetryCountColumn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", ""); err != nil {
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

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 7, "t", "", "archie,bug"); err != nil {
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
