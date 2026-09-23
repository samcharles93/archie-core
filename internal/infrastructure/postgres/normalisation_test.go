package postgres

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func storeFor(t *testing.T) *Store {
	t.Helper()
	pool := openPool(t, pgtest.URL(t))
	if err := Migrate(t.Context(), pool, Migrations()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return New(pool)
}

// runningTask enqueues one issue and claims it, the state every park starts in.
func runningTask(t *testing.T, s *Store) *workflow.Task {
	t.Helper()
	if _, err := s.EnqueueIssue(t.Context(), "acme", "widgets", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(t.Context())
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = %v, %v", task, err)
	}
	return task
}

type transitionRow struct{ from, to, detail string }

func transitions(t *testing.T, s *Store, taskID int64) []transitionRow {
	t.Helper()
	rows, err := s.pool.Query(t.Context(), "SELECT from_status, to_status, detail FROM transitions WHERE task_id = $1 ORDER BY id", taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []transitionRow
	for rows.Next() {
		var r transitionRow
		if err := rows.Scan(&r.from, &r.to, &r.detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// Every free-text column the SQLite store clipped to 4000 bytes on a rune
// boundary is clipped the same way here. The oversized value ends in a
// multi-byte rune straddling the limit, so a byte cut would store invalid
// UTF-8 and a missing clip would store all of it.
func TestWritesClipFreeTextTo4000Bytes(t *testing.T) {
	oversized := strings.Repeat("x", 3999) + "é" + strings.Repeat("y", 1000)
	want := strings.Repeat("x", 3999)

	tests := []struct {
		name  string
		write func(t *testing.T, s *Store) string
	}{
		{name: "transition to parked: park reason and transition detail", write: func(t *testing.T, s *Store) string {
			task := runningTask(t, s)
			if err := s.Transition(t.Context(), task.ID, workflow.StatusRunning, workflow.StatusParked, oversized); err != nil {
				t.Fatal(err)
			}
			rows := transitions(t, s, task.ID)
			if len(rows) == 0 || rows[len(rows)-1].detail != want {
				t.Errorf("transition detail not clipped: %d rows", len(rows))
			}
			got, _ := s.TaskByID(t.Context(), task.ID)
			return got.ParkReason
		}},
		{name: "park task: park reason and transition detail", write: func(t *testing.T, s *Store) string {
			task := runningTask(t, s)
			if err := s.ParkTask(t.Context(), task.ID, workflow.StatusRunning, oversized, "transient"); err != nil {
				t.Fatal(err)
			}
			rows := transitions(t, s, task.ID)
			if len(rows) == 0 || rows[len(rows)-1].detail != want {
				t.Errorf("transition detail not clipped: %d rows", len(rows))
			}
			got, _ := s.TaskByID(t.Context(), task.ID)
			return got.ParkReason
		}},
		{name: "update: park reason", write: func(t *testing.T, s *Store) string {
			task := runningTask(t, s)
			task.ParkReason = oversized
			if err := s.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			got, _ := s.TaskByID(t.Context(), task.ID)
			return got.ParkReason
		}},
		{name: "begin remediation: review payload", write: func(t *testing.T, s *Store) string {
			task := runningTask(t, s)
			if err := s.Transition(t.Context(), task.ID, workflow.StatusRunning, workflow.StatusPROpen, "pr"); err != nil {
				t.Fatal(err)
			}
			if err := s.BeginRemediation(t.Context(), task.ID, oversized); err != nil {
				t.Fatal(err)
			}
			got, _ := s.TaskByID(t.Context(), task.ID)
			return got.ReviewPayload
		}},
		{name: "update review payload", write: func(t *testing.T, s *Store) string {
			task := runningTask(t, s)
			if err := s.Transition(t.Context(), task.ID, workflow.StatusRunning, workflow.StatusPROpen, "pr"); err != nil {
				t.Fatal(err)
			}
			if err := s.BeginRemediation(t.Context(), task.ID, "first"); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateReviewPayload(t.Context(), task.ID, oversized); err != nil {
				t.Fatal(err)
			}
			got, _ := s.TaskByID(t.Context(), task.ID)
			return got.ReviewPayload
		}},
		{name: "event detail", write: func(t *testing.T, s *Store) string {
			if _, err := s.InsertEvent(t.Context(), events.Event{Kind: events.KindLog, Detail: oversized}); err != nil {
				t.Fatal(err)
			}
			got, err := s.EventsSince(t.Context(), "", 1)
			if err != nil || len(got) != 1 {
				t.Fatalf("EventsSince = %d, %v", len(got), err)
			}
			return got[0].Detail
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.write(t, storeFor(t))
			if got != want || !utf8.ValidString(got) {
				t.Errorf("stored %d bytes (valid UTF-8 %v), want the %d-byte rune-boundary clip", len(got), utf8.ValidString(got), len(want))
			}
		})
	}
}

func TestParkClassNormalisation(t *testing.T) {
	tests := []struct {
		name string
		park func(ctx context.Context, s *Store, id int64) error
		want string
	}{
		{name: "generic transition to parked is needs_human", want: "needs_human", park: func(ctx context.Context, s *Store, id int64) error {
			return s.Transition(ctx, id, workflow.StatusRunning, workflow.StatusParked, "gate failed")
		}},
		{name: "known class is kept", want: "transient", park: func(ctx context.Context, s *Store, id int64) error {
			return s.ParkTask(ctx, id, workflow.StatusRunning, "container acquire failed", "transient")
		}},
		{name: "unknown class falls back to needs_human", want: "needs_human", park: func(ctx context.Context, s *Store, id int64) error {
			return s.ParkTask(ctx, id, workflow.StatusRunning, "x", "urgent")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := storeFor(t)
			task := runningTask(t, s)
			if err := tt.park(t.Context(), s, task.ID); err != nil {
				t.Fatal(err)
			}
			got, _ := s.TaskByID(t.Context(), task.ID)
			if got.Status != workflow.StatusParked || got.ParkClass != tt.want {
				t.Fatalf("status %q class %q, want parked %q", got.Status, got.ParkClass, tt.want)
			}
			// Requeue clears class and reason together: a stale class on a
			// queued task would group a live task with parked ones.
			if err := s.Requeue(t.Context(), task.ID, workflow.StatusParked, "implement"); err != nil {
				t.Fatal(err)
			}
			got, _ = s.TaskByID(t.Context(), task.ID)
			if got.ParkClass != "needs_human" || got.ParkReason != "" {
				t.Errorf("after requeue: class %q reason %q, want cleared", got.ParkClass, got.ParkReason)
			}
		})
	}
}

// A transition that leaves parked alone keeps the previous park fields, and
// every guarded write leaves exactly one audit row in order.
func TestTransitionsRecordEachMoveInOrder(t *testing.T) {
	s := storeFor(t)
	task := runningTask(t, s)
	ctx := t.Context()
	if err := s.ParkTask(ctx, task.ID, workflow.StatusRunning, "why", "transient"); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, task.ID, workflow.StatusParked, "implement"); err != nil {
		t.Fatal(err)
	}
	// A stale transition writes no audit row.
	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusPROpen, "stale"); err == nil {
		t.Fatal("stale Transition succeeded")
	}
	got := transitions(t, s, task.ID)
	want := []transitionRow{
		{workflow.StatusRunning, workflow.StatusParked, "why"},
		{workflow.StatusParked, workflow.StatusQueued, "requeued implement"},
	}
	if len(got) != len(want) {
		t.Fatalf("transitions = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("transition %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
