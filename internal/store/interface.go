// Package store defines the storage abstractions for archied's task and
// event data. TaskStore is the full surface the daemon needs; *Store (SQLite)
// and the nell adapter both satisfy it. Consumers depend on the interface,
// never on a concrete backend.
package store

import (
	"context"

	"github.com/samcharles93/archie-core/internal/events"
)

// TaskStore is the full store surface the daemon needs.
// Every storage backend  --  SQLite, NellDB, future additions  --  implements this
// interface. Consumers depend on TaskStore, never on a concrete type.
type TaskStore interface {
	TaskLifecycle
	TaskEvents
	TaskQueries
}

// TaskLifecycle manages the core task state machine and enqueuing.
type TaskLifecycle interface {
	EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error)
	EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string, issueNumber int) (*Task, error)
	ClaimNext(ctx context.Context) (*Task, error)
	ClaimByIssue(ctx context.Context, owner, repo string, number int) (*Task, error)
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
	Update(ctx context.Context, t *Task) error
	Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error
	RecoverStale(ctx context.Context) (int64, error)
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
// mid-run. *Store, storerpc.Client, and the nell adapter all satisfy it.
// This is the interface archie-agent uses via storerpc NATS proxy.
type WorkflowStore interface {
	Update(ctx context.Context, t *Task) error
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
}

// Compile-time check: *Store satisfies TaskStore.
var _ TaskStore = (*Store)(nil)

// Compile-time check: *Store satisfies WorkflowStore.
var _ WorkflowStore = (*Store)(nil)
