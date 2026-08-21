// Package store defines the storage abstractions for archied's task and
// event data. TaskStore is the full surface the daemon needs and *Store is its
// SQLite implementation.
package store

import (
	"context"
	"time"

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

// Compile-time check: *Store satisfies TaskStore.
var _ TaskStore = (*Store)(nil)

// Compile-time check: *Store satisfies WorkflowStore.
var _ WorkflowStore = (*Store)(nil)

// Compile-time check: *Store satisfies CaptureStore.
var _ CaptureStore = (*Store)(nil)
