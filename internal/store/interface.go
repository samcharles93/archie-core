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
// Every storage backend — SQLite, NellDB, future additions — implements this
// interface. Consumers depend on TaskStore, never on a concrete type.
type TaskStore interface {
	// ── Task lifecycle ──────────────────────────────────────────────

	EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels string) (bool, error)
	ClaimNext(ctx context.Context) (*Task, error)
	ClaimByIssue(ctx context.Context, owner, repo string, number int) (*Task, error)
	TaskByIssue(ctx context.Context, owner, repo string, number int) (*Task, error)
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
	Update(ctx context.Context, t *Task) error
	Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error
	RecoverStale(ctx context.Context) (int64, error)
	IncrementRetryCount(ctx context.Context, taskID int64) error

	// ── Queries ─────────────────────────────────────────────────────

	WaitingTasks(ctx context.Context) ([]Task, error)
	OpenPRs(ctx context.Context) ([]Task, error)
	ClearTerminalTasks(ctx context.Context) (int64, error)

	// ── Observability ───────────────────────────────────────────────

	InsertEvent(ctx context.Context, e events.Event) (int64, error)
	EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error)
	RecentEvents(ctx context.Context, limit int) ([]events.Event, error)
	TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error)
	Tasks(ctx context.Context, limit int) ([]Task, error)
	StatusCounts(ctx context.Context) (map[string]int, error)
	WorkflowStats(ctx context.Context) ([]WorkflowStat, error)
	StageStats(ctx context.Context) ([]StageStat, error)
	TokensByDay(ctx context.Context, days int) ([]DayTokens, error)

	// ── Lifecycle ───────────────────────────────────────────────────

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
