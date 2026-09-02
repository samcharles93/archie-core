// Package store defines the storage abstractions for archied's task and
// event data. TaskStore is the full surface the daemon needs and *Store is its
// SQLite implementation.
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/events"
)

// TaskStore is the full store surface the daemon needs.
// Consumers depend on TaskStore, never on a concrete type.
type TaskStore interface {
	TaskLifecycle
	TaskEvents
	TaskQueries
	TaskArchiver
	TaskRetryer
}

// TaskLifecycle manages the core task state machine and enqueuing.
// Binding-triggered task creation lives on BindingTaskCreator, not here,
// so the lifecycle surface stays narrow and non-binding consumers (forge
// poll, chat spawn, drain loop) do not acquire a binding-specific shape.
type TaskLifecycle interface {
	EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error)
	EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string) (*Task, error)
	ClaimNext(ctx context.Context) (*Task, error)
	ClaimByIssue(ctx context.Context, owner, repo string, number int) (*Task, error)
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
	Update(ctx context.Context, t *Task) error
	Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error
	RecoverStale(ctx context.Context) (int64, error)
}

// TaskArchiver removes one terminal task's local record with an optimistic
// status guard. It is separate from the already broad lifecycle contract so
// consumers that only run tasks do not acquire an operator-only capability.
type TaskArchiver interface {
	ArchiveTask(ctx context.Context, taskID int64, fromStatus string, audit events.Event) (eventID int64, err error)
}

// TaskRetryer atomically requeues recoverable work and accounts for the new
// attempt so a partial write cannot evade the retry cap.
type TaskRetryer interface {
	RetryTask(ctx context.Context, taskID int64, fromStatus, workflow string) error
}

// TaskQueries groups read-only task accessors.
type TaskQueries interface {
	TaskByIssue(ctx context.Context, owner, repo string, number int) (*Task, error)
	TaskByID(ctx context.Context, taskID int64) (*Task, error)
	OpenPRs(ctx context.Context) ([]Task, error)
	ClearTerminalTasks(ctx context.Context) (int64, error)
	Tasks(ctx context.Context, limit int) ([]Task, error)
	StatusCounts(ctx context.Context) (map[string]int, error)
	IncrementRetryCount(ctx context.Context, taskID int64) error
}

// TaskEvents groups observability and lifecycle methods.
type TaskEvents interface {
	InsertEvent(ctx context.Context, e events.Event) (int64, error)
	EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error)
	TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error)
	WorkflowStats(ctx context.Context) ([]WorkflowStat, error)
	StageStats(ctx context.Context) ([]StageStat, error)
	TokensByDay(ctx context.Context, days int) ([]DayTokens, error)
	Close() error
}

// WorkflowStore is the narrow subset of TaskStore that workflow stages call
// mid-run. *Store and storerpc.Client satisfy it.
// This is the interface archie-agent uses via storerpc NATS proxy.
type WorkflowStore interface {
	Update(ctx context.Context, t *Task) error
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
	InsertEvent(ctx context.Context, e events.Event) (int64, error)
}

// CaptureStore persists unbound inbound webhook captures -- events with no
// workflow binding and no task association. Deliberately separate from
// TaskStore: a consumer that only needs capture (the intake HTTP handler)
// should not acquire the full task-lifecycle surface, mirroring why
// TaskArchiver is split out above. See docs/prds/event-capture-storage.md.
type CaptureStore interface {
	InsertCapture(ctx context.Context, c CapturedEvent, retention time.Duration, maxEvents int) (int64, error)
	ListCaptures(ctx context.Context, limit int) ([]CapturedEvent, error)
}

// MappingStore persists payload field mappings (t2db.3). Deliberately
// separate from TaskStore and CaptureStore for the same reason those are
// split: the dashboard's mapping editor should only acquire the mapping
// surface, not the full task or capture APIs. See
// docs/prds/payload-field-mapping.md.
type MappingStore interface {
	InsertMapping(ctx context.Context, m mapping.Mapping) (int64, error)
	GetMapping(ctx context.Context, id int64) (*mapping.Mapping, error)
	ListMappings(ctx context.Context) ([]mapping.Mapping, error)
	UpdateMapping(ctx context.Context, m mapping.Mapping) error
	DeleteMapping(ctx context.Context, id int64) error
}

// BindingStore persists playbook bindings (t2db.4 Phase B): CRUD and the
// draft -> pending_approval -> armed state machine. Split off from
// TaskStore and MappingStore so the webui binding editor does not acquire
// the full task or mapping surfaces. Dispatch-time helpers live in
// BindingDispatcher; the daemon depends on both. See
// docs/prds/playbook-binding.md.
type BindingStore interface {
	InsertBinding(ctx context.Context, b binding.Binding) (int64, error)
	GetBinding(ctx context.Context, id int64) (*binding.Binding, error)
	ListBindings(ctx context.Context) ([]binding.Binding, error)
	UpdateBinding(ctx context.Context, b binding.Binding) error
	DeleteBinding(ctx context.Context, id int64) error
	ApproveBinding(ctx context.Context, id int64) error
}

// BindingDispatcher is the dispatch-loop surface over the bindings store:
// look up armed bindings for HMAC verification, list captures that still
// need dispatching, and record the at-most-once ledger row. The
// dispatcher's task-creation call (EnqueueBindingTask) lives on a separate
// BindingTaskCreator interface so the dispatcher does not depend on the
// full TaskStore just to spawn one task.
type BindingDispatcher interface {
	ArmedBindingsForSource(ctx context.Context, source string) ([]binding.Binding, error)
	RecordDispatch(
		ctx context.Context,
		tx *sql.Tx,
		bindingID int64,
		bindingVersion int64,
		captureID int64,
		taskID int64,
	) error
	ListUndispatchedCaptures(ctx context.Context, sources []string, limit int) ([]CapturedEvent, error)
}

// BindingTaskCreator is the single-method consumer-facing surface for
// enqueueing a task triggered by a binding. Splitting it off TaskLifecycle
// keeps the lifecycle surface narrow (8 methods, the interfacebloat limit)
// and keeps the binding-specific shape on the binding interfaces.
type BindingTaskCreator interface {
	EnqueueBindingTask(ctx context.Context, owner, repo, title, body, workflow, identity string, bindingID int64, bindingVersion int) (*Task, error)
}

// Compile-time check: *Store satisfies TaskStore.
var _ TaskStore = (*Store)(nil)

// Compile-time check: *Store satisfies WorkflowStore.
var _ WorkflowStore = (*Store)(nil)

// Compile-time check: *Store satisfies CaptureStore.
var _ CaptureStore = (*Store)(nil)

// Compile-time check: *Store satisfies MappingStore.
var _ MappingStore = (*Store)(nil)

// Compile-time check: *Store satisfies BindingStore.
var _ BindingStore = (*Store)(nil)

// Compile-time check: *Store satisfies BindingDispatcher.
var _ BindingDispatcher = (*Store)(nil)

// Compile-time check: *Store satisfies BindingTaskCreator.
var _ BindingTaskCreator = (*Store)(nil)
