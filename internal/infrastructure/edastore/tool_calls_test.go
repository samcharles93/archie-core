package edastore_test

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
)

func insertToolCall(t *testing.T, s *edastore.Store, tc edastore.ToolCall) {
	t.Helper()
	if err := s.InsertToolCall(t.Context(), tc); err != nil {
		t.Fatalf("InsertToolCall() error = %v", err)
	}
}

// TestInsertToolCallRoundTrip pins the write/read pair on the tool_calls
// collection: a row written with the fields the tool_call projection provides
// reads back unchanged, and the read filters on task_id so one task's calls
// never surface in another's.
func TestInsertToolCallRoundTrip(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)

	insertToolCall(t, s, edastore.ToolCall{
		TaskID:   42,
		Attempt:  2,
		Tool:     "shell",
		Result:   "all checks passed",
		CalledAt: at,
	})
	insertToolCall(t, s, edastore.ToolCall{
		TaskID:   42,
		Attempt:  2,
		Tool:     "read_file",
		Error:    "no such file",
		CalledAt: at.Add(time.Second),
	})
	// A different task's call must not leak into the read below.
	insertToolCall(t, s, edastore.ToolCall{
		TaskID:   7,
		Attempt:  1,
		Tool:     "shell",
		Result:   "other task's call",
		CalledAt: at.Add(2 * time.Second),
	})

	got, err := s.TaskToolCalls(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskToolCalls() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("TaskToolCalls() returned %d rows for task 42, want 2: %+v", len(got), got)
	}

	first, second := got[0], got[1]
	// Chronological order: the earlier call first.
	if first.Tool != "shell" || second.Tool != "read_file" {
		t.Errorf("order = [%q, %q], want [shell, read_file] by called_at", first.Tool, second.Tool)
	}
	if first.TaskID != 42 || first.Attempt != 2 {
		t.Errorf("first row task/attempt = %d/%d, want 42/2", first.TaskID, first.Attempt)
	}
	if first.Result != "all checks passed" {
		t.Errorf("first row result = %q, want the stored summary without JSON quoting", first.Result)
	}
	if first.Error != "" {
		t.Errorf("first row error = %q, want empty: a succeeded call is not an error", first.Error)
	}
	if second.Error != "no such file" {
		t.Errorf("second row error = %q, want the stored failure detail", second.Error)
	}
	if second.Result != "" {
		t.Errorf("second row result = %q, want empty: a failed call has no result", second.Result)
	}
	if first.CalledAt.IsZero() {
		t.Error("first row called_at is zero; the call time must round-trip")
	}
}

// TestInsertToolCallIsAnAppendOnlyTranscript pins that the collection is one
// row per tool call, not an upsert: repeated calls with the same (task,
// attempt, tool) are distinct transcript entries. There is no unique index and
// there must not be one -- the SQLite predecessor had none (tool calls lived
// only in the on-disk task log), so the schema's non-unique (task_id, attempt)
// index stays the only one.
func TestInsertToolCallIsAnAppendOnlyTranscript(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	row := edastore.ToolCall{TaskID: 42, Attempt: 3, Tool: "shell", Result: "ok", CalledAt: at}

	insertToolCall(t, s, row)
	insertToolCall(t, s, row)

	got, err := s.TaskToolCalls(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskToolCalls() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("TaskToolCalls() returned %d rows, want 2: the transcript is append-only, not a dedup ledger", len(got))
	}
}
