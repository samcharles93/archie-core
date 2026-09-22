package archied

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
)

// toolCallWriter is the edastore surface the projection writes through. Kept
// narrow so a test can fail it without touching the real store.
type toolCallWriter interface {
	InsertToolCall(ctx context.Context, tc edastore.ToolCall) error
}

// toolCallProjectingTaskStore decorates the State Store's task store so every
// persisted tool_call event also lands in the tool_calls collection on the
// event-capture store, which this same process owns. The task event log stays
// the source of truth; the collection is the queryable transcript of it.
//
// A projection failure is NEVER returned: event persistence is load-bearing,
// the collection is observability, and failing a task write because its
// transcript copy failed would trade the fact for its shadow. The failure is
// instead counted and logged with the running count, so it is visible without
// ever changing InsertEvent's outcome.
type toolCallProjectingTaskStore struct {
	storecontract.TaskStore
	toolCalls          toolCallWriter
	log                *slog.Logger
	projectionFailures atomic.Int64
}

func newToolCallProjectingTaskStore(inner storecontract.TaskStore, toolCalls toolCallWriter, log *slog.Logger) *toolCallProjectingTaskStore {
	if log == nil {
		log = slog.Default()
	}
	return &toolCallProjectingTaskStore{TaskStore: inner, toolCalls: toolCalls, log: log}
}

// toolCallProjectionFailures reports how many tool_call projections have been
// dropped since this process started. Named and exported to the test surface
// rather than buried in a log field alone: a silent swallow is the bug this
// decorator exists not to have.
func (s *toolCallProjectingTaskStore) toolCallProjectionFailures() int64 {
	return s.projectionFailures.Load()
}

func (s *toolCallProjectingTaskStore) InsertEvent(ctx context.Context, e events.Event) (int64, error) {
	id, err := s.TaskStore.InsertEvent(ctx, e)
	if err != nil || e.Kind != events.KindToolCall {
		return id, err
	}
	if perr := s.toolCalls.InsertToolCall(ctx, toolCallFromEvent(e)); perr != nil {
		failures := s.projectionFailures.Add(1)
		s.log.Error("tool_call projection failed; the event persisted but the tool_calls row did not",
			"err", perr, "task_id", e.TaskID, "attempt", e.Attempt,
			"tool", e.Data["tool"], "projection_failures", failures)
	}
	return id, nil
}

// toolCallFromEvent adapts a tool_call event to the existing tool_calls
// collection. The event carries the tool name and the failure flag in data and
// the one-line outcome in detail: a succeeded call's summary is its result, a
// failed call's summary is its error. The collection's args and duration_ms
// fields stay unwritten -- the event reports neither -- so they are schema
// reserved for a producer that one day reports them, not promises this
// projection makes.
func toolCallFromEvent(e events.Event) edastore.ToolCall {
	tool, _ := e.Data["tool"].(string)
	failed, _ := e.Data["failed"].(bool)
	tc := edastore.ToolCall{
		TaskID:   e.TaskID,
		Attempt:  e.Attempt,
		Tool:     tool,
		CalledAt: e.At,
	}
	if failed {
		tc.Error = e.Detail
	} else {
		tc.Result = e.Detail
	}
	return tc
}
