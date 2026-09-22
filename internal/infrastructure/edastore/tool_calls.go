package edastore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// ToolCall is one completed tool invocation during an agent stage run, as the
// tool_call event projection stores it. The projection adapts the event to the
// existing collection (schema.go): a tool's output summary goes in result, a
// failure's detail in error. The args and duration_ms fields the schema also
// carries are left unwritten -- the tool_call event reports neither -- and a
// future producer that has them can start filling them without a schema
// change.
type ToolCall struct {
	ID       string
	TaskID   int64
	Attempt  int
	Tool     string
	Result   string
	Error    string
	CalledAt time.Time
}

// InsertToolCall appends one row to the tool_calls collection. There is no
// idempotency here, deliberately: the collection is an append-only transcript
// and the schema carries no unique index (the SQLite predecessor had none --
// tool calls lived only in the on-disk task log), so one event is one row.
//
// The notify hook stays silent, matching the task-dispatch tables rather than
// the operator-editable ones: tool calls are high-frequency telemetry, not
// dashboard activity.
func (s *Store) InsertToolCall(_ context.Context, tc ToolCall) error {
	collection, err := s.app.FindCollectionByNameOrId(CollToolCalls)
	if err != nil {
		return err
	}
	r := core.NewRecord(collection)
	r.Set("task_id", tc.TaskID)
	r.Set("attempt", tc.Attempt)
	r.Set("tool", tc.Tool)
	if tc.Result != "" {
		r.Set("result", tc.Result)
	}
	if tc.Error != "" {
		r.Set("error", tc.Error)
	}
	if !tc.CalledAt.IsZero() {
		// called_at is an autodate field, but an explicitly set date is kept:
		// the row carries the time the tool call happened, not the time it
		// was projected.
		r.Set("called_at", tc.CalledAt)
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("edastore: insert tool call: %w", err)
	}
	return nil
}

// TaskToolCalls returns one task's tool calls in call order.
func (s *Store) TaskToolCalls(_ context.Context, taskID int64) ([]ToolCall, error) {
	records, err := s.app.FindRecordsByFilter(CollToolCalls, "task_id = {:task}", "+called_at", 0, 0,
		map[string]any{"task": taskID})
	if err != nil {
		return nil, fmt.Errorf("edastore: list tool calls: %w", err)
	}
	out := make([]ToolCall, 0, len(records))
	for _, r := range records {
		out = append(out, ToolCall{
			ID:       r.Id,
			TaskID:   r.GetInt64("task_id"),
			Attempt:  r.GetInt("attempt"),
			Tool:     r.GetString("tool"),
			Result:   jsonTextToString(r.GetString("result")),
			Error:    r.GetString("error"),
			CalledAt: r.GetDateTime("called_at").Time(),
		})
	}
	return out, nil
}

// jsonTextToString unwraps the JSON string a JSONField stores a plain text
// summary under, and falls back to the raw text for anything that is not a
// JSON string (e.g. an object a future producer stored directly).
func jsonTextToString(raw string) string {
	if raw == "" {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw
	}
	return decoded
}
