// Package task is the task-execution vocabulary of the workflow domain:
// the Task record, its lifecycle statuses, and the narrow Store contract
// workflow stages call mid-run. It split out of package workflow
// (archie-core-8cda.5.6) so the UI process can hold the vocabulary without
// linking the pipeline engine there -- which drags in agentexec, skill and
// tools. package workflow keeps aliases, so daemon-side callers are
// unaffected.
package task

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Task lifecycle statuses. Workflows move tasks between them; the
// daemon owns queued→running claims and crash recovery.
const (
	// Defined in internal/taskstate so the dashboard, chat and the store
	// share one vocabulary. These names are kept as the workflow domain's
	// public spelling; the values live in one place.
	StatusQueued       = taskstate.Queued
	StatusRunning      = taskstate.Running
	StatusWaitingHuman = taskstate.WaitingHuman
	StatusPROpen       = taskstate.PROpen
	StatusMerged       = taskstate.Merged
	StatusParked       = taskstate.Parked
	StatusDead         = taskstate.Dead
	StatusRejected     = taskstate.Rejected
	StatusClosedWontDo = taskstate.Declined
)

// Task is the task-execution record the workflow domain operates on.
// internal/store persists this type (persistence -> domain).
type Task struct {
	ID          int64  `json:"id"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issue_number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Labels      string `json:"labels"` // comma-separated, as seen at enqueue time
	Status      string `json:"status"`
	Workflow    string `json:"workflow"`
	Stage       string `json:"stage"`
	Branch      string `json:"branch"`
	Plan        string `json:"plan"`
	Notes       string `json:"notes"`
	PRNumber    int    `json:"pr_number"`
	TokensUsed  int    `json:"tokens_used"`
	Iterations  int    `json:"iterations"`
	Attempt     int    `json:"attempt"`
	ParkReason  string `json:"park_reason"`
	// RetryCount tracks how many times a parked task has been retried
	// (parking-to-queued transitions). When it reaches the configured
	// max_retries the daemon moves the task to StatusDead.
	RetryCount int `json:"retry_count"`
	// WatchCommentID: replies to the issue after this comment are the
	// human input a waiting_human task is blocked on.
	WatchCommentID int64 `json:"watch_comment_id"`
	// Source is "forge" (default; a real forge issue backs this task) or
	// "chat" (created via /spawn with no forge issue). Workflow stages
	// and daemon reconciliation must skip forge-only operations (issue
	// labels, comments, replies) for chat-sourced tasks; IssueNumber on
	// a chat task is a synthetic value, not a real forge issue.
	Source string `json:"source"`
	// Identity is the archie identity that owns this task (chat-spawned
	// tasks only; empty for forge-sourced tasks and single-identity
	// deployments). Used to scope /approve and /cancel authorization so
	// one identity cannot control another's chat-spawned tasks.
	Identity string `json:"identity"`
	// BindingID and BindingVersion are stamped when a task was created
	// from a playbook binding dispatch (t2db.4 Phase B). They record
	// provenance: which binding fired this task and at what version, so
	// later edits to the binding cannot silently rewrite history.
	BindingID      int64 `json:"binding_id"`
	BindingVersion int   `json:"binding_version"`
	// CreatedAt and UpdatedAt are the SQLite row timestamps, exposed so
	// callers can show a task's age and last activity. They are written by
	// column defaults and the UPDATE statements, never by the caller.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SourceForge and SourceChat are the valid values for Task.Source.
// Legacy rows and forge-polled tasks have Source == SourceForge (the
// zero value defaults there via the SQLite column default).
const (
	SourceForge = "forge"
	SourceChat  = "chat"
)

// IsForgeBacked reports whether t corresponds to a real forge issue.
// Chat-spawned tasks (Source == SourceChat) have a synthetic
// IssueNumber and must not be used in forge label/comment/reply calls.
func (t Task) IsForgeBacked() bool {
	return t.Source != SourceChat
}

// Store is the narrow, consumer-owned subset of the State Store contract
// that workflow stages call mid-run. *store.Store and staterpc.Client
// satisfy it; this is the interface archie-agent uses over the gRPC State
// Store contract. It supersedes store.WorkflowStore.
type Store interface {
	Update(ctx context.Context, t *Task) error
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
	InsertEvent(ctx context.Context, e events.Event) (int64, error)
}

// Definition is the operator-safe snapshot of an executable workflow. It
// contains only identity and stage order, never executable function values.
// It lives with the task vocabulary (not with the engine in package
// workflow) for the same reason this package exists: dashboards render
// Definitions without linking the engine.
type Definition struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Origin  string   `json:"origin"`
	Enabled bool     `json:"enabled"`
	Stages  []string `json:"stages"`
}
