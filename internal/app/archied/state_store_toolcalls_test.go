package archied

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

func toolCallEvent(taskID int64, attempt int) events.Event {
	return events.Event{
		Kind:     events.KindToolCall,
		TaskID:   taskID,
		Attempt:  attempt,
		Stage:    "implement",
		Detail:   "all checks passed",
		Data:     map[string]any{"tool": "shell", "failed": false},
		At:       time.Now().UTC(),
		Workflow: "tdd",
	}
}

// TestInsertEventProjectsToolCall pins the projection (test a): one tool_call
// event through the decorated store persists the event AND exactly one
// tool_calls row, with the row's fields taken from the event -- the tool name
// from data, the output summary in result for a succeeded call.
func TestInsertEventProjectsToolCall(t *testing.T) {
	st := store.OpenTest(t)
	eda := edastore.OpenTest(t)
	ts := newToolCallProjectingTaskStore(st, eda, slog.Default())

	id, err := ts.InsertEvent(t.Context(), toolCallEvent(42, 2))
	if err != nil {
		t.Fatalf("InsertEvent() error = %v", err)
	}
	if id == 0 {
		t.Fatal("InsertEvent() returned id 0; the event itself must still persist")
	}

	rows, err := eda.TaskToolCalls(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskToolCalls() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("TaskToolCalls() returned %d rows, want exactly one per tool_call event: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.TaskID != 42 || row.Attempt != 2 {
		t.Errorf("row task/attempt = %d/%d, want 42/2", row.TaskID, row.Attempt)
	}
	if row.Tool != "shell" {
		t.Errorf("row tool = %q, want \"shell\" from the event's data", row.Tool)
	}
	if row.Result != "all checks passed" {
		t.Errorf("row result = %q, want the event detail of a succeeded call", row.Result)
	}
	if row.Error != "" {
		t.Errorf("row error = %q, want empty for a succeeded call", row.Error)
	}
	if row.CalledAt.IsZero() {
		t.Error("row called_at is zero; it must carry the event time")
	}
}

// TestInsertEventProjectsFailedToolCall pins the failure column: a tool_call
// event whose call failed stores its detail under error, not result, so the
// transcript can distinguish the two without re-parsing text.
func TestInsertEventProjectsFailedToolCall(t *testing.T) {
	st := store.OpenTest(t)
	eda := edastore.OpenTest(t)
	ts := newToolCallProjectingTaskStore(st, eda, slog.Default())

	e := toolCallEvent(42, 2)
	e.Detail = "error: no such file"
	e.Data = map[string]any{"tool": "read_file", "failed": true}
	if _, err := ts.InsertEvent(t.Context(), e); err != nil {
		t.Fatalf("InsertEvent() error = %v", err)
	}

	rows, err := eda.TaskToolCalls(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskToolCalls() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("TaskToolCalls() returned %d rows, want 1", len(rows))
	}
	if rows[0].Error != "error: no such file" {
		t.Errorf("row error = %q, want the failed call's detail", rows[0].Error)
	}
	if rows[0].Result != "" {
		t.Errorf("row result = %q, want empty for a failed call", rows[0].Result)
	}
}

// TestInsertEventLeavesOtherKindsUnprojected pins the kind gate: only
// tool_call events reach the collection; the task timeline's other kinds must
// not grow tool_calls rows.
func TestInsertEventLeavesOtherKindsUnprojected(t *testing.T) {
	st := store.OpenTest(t)
	eda := edastore.OpenTest(t)
	ts := newToolCallProjectingTaskStore(st, eda, slog.Default())

	if _, err := ts.InsertEvent(t.Context(), events.Event{
		Kind: events.KindStageFinish, TaskID: 42, Attempt: 2,
		Data: map[string]any{"duration_ms": 12.0},
	}); err != nil {
		t.Fatalf("InsertEvent() error = %v", err)
	}

	rows, err := eda.TaskToolCalls(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskToolCalls() error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("TaskToolCalls() returned %d rows for a stage_finish event, want 0", len(rows))
	}
}

// failingToolCallWriter stands in for a broken PocketBase side, so the
// never-returned-failure contract can be tested without sabotaging the real
// store.
type failingToolCallWriter struct{ err error }

func (f failingToolCallWriter) InsertToolCall(context.Context, edastore.ToolCall) error {
	return f.err
}

// TestInsertEventSucceedsWhenProjectionFails pins the load-bearing half of the
// contract: a tool_calls write failure is never returned from InsertEvent --
// event persistence is the contract, the projection is observability -- and
// the failure is recorded, not swallowed silently.
func TestInsertEventSucceedsWhenProjectionFails(t *testing.T) {
	st := store.OpenTest(t)
	var buf bytes.Buffer
	ts := newToolCallProjectingTaskStore(st, failingToolCallWriter{
		err: errors.New("disk on fire"),
	}, slog.New(slog.NewTextHandler(&buf, nil)))

	id, err := ts.InsertEvent(t.Context(), toolCallEvent(42, 2))
	if err != nil {
		t.Fatalf("InsertEvent() error = %v; a projection failure must never fail the event write", err)
	}
	if id == 0 {
		t.Fatal("InsertEvent() returned id 0; the event must still persist")
	}
	if got := ts.toolCallProjectionFailures(); got != 1 {
		t.Errorf("toolCallProjectionFailures() = %d, want 1: the failure must be counted, not swallowed", got)
	}
	if !strings.Contains(buf.String(), "disk on fire") {
		t.Errorf("log output %q does not carry the projection failure; it was swallowed silently", buf.String())
	}
	// The event row is what the whole surface exists for -- it must be there.
	persisted, err := st.TaskEvents(t.Context(), 42)
	if err != nil {
		t.Fatalf("TaskEvents() error = %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("TaskEvents() returned %d events, want 1: the event persisted despite the projection failure", len(persisted))
	}
}

// TestStateStoreDepsProjectToolCalls pins the wire-in: with both stores open,
// the composition serves Tasks as the projecting decorator; with no
// event-capture store, Tasks is passed through untouched rather than wrapped
// around a nil writer.
func TestStateStoreDepsProjectToolCalls(t *testing.T) {
	st := store.OpenTest(t)
	eda := edastore.OpenTest(t)

	b := newBootstrap()
	b.st = st
	b.eda = eda
	deps := b.stateStoreDeps(&staterpc.TaskGrants{})
	if _, ok := deps.Tasks.(*toolCallProjectingTaskStore); !ok {
		t.Fatalf("deps.Tasks = %T, want the tool_call projecting decorator", deps.Tasks)
	}

	plain := newBootstrap()
	plain.st = st
	plainDeps := plain.stateStoreDeps(&staterpc.TaskGrants{})
	if _, ok := plainDeps.Tasks.(*toolCallProjectingTaskStore); ok {
		t.Fatal("deps.Tasks is decorated with no event-capture store on the boot; the projection would write through a nil writer")
	}
}
