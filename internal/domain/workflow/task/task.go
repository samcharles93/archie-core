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

	"github.com/samcharles93/archie-core/internal/domain/org"
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
	StatusCompleted    = taskstate.Completed
)

// Task is the task-execution record the workflow domain operates on.
// internal/infrastructure/postgres persists this type (persistence -> domain).
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
	// WorkflowDefinitionVersion and WorkflowDefinitionDigest pin the exact
	// database definition selected for this execution. WorkflowDefinitionYAML
	// is the immutable worker handoff; retries reuse it instead of re-reading
	// the active control-plane resource.
	WorkflowDefinitionVersion int64  `json:"workflow_definition_version"`
	WorkflowDefinitionDigest  string `json:"workflow_definition_digest"`
	WorkflowDefinitionYAML    string `json:"workflow_definition_yaml"`
	Stage                     string `json:"stage"`
	Branch                    string `json:"branch"`
	Plan                      string `json:"plan"`
	Notes                     string `json:"notes"`
	PRNumber                  int    `json:"pr_number"`
	TokensUsed                int    `json:"tokens_used"`
	Iterations                int    `json:"iterations"`
	Attempt                   int    `json:"attempt"`
	ParkReason                string `json:"park_reason"`
	// RetryCount tracks how many times a parked task has been retried
	// (parking-to-queued transitions). When it reaches the configured
	// max_retries the daemon moves the task to StatusDead.
	RetryCount int `json:"retry_count"`
	// WatchCommentID is the poll backstop's high-water mark over forge
	// review-comment IDs for this task (pr-review-remediation.md decision
	// 2's persisted per-task cursor; the comment-watch design it was
	// created for never shipped, and the field carries the cursor now).
	WatchCommentID int64 `json:"watch_comment_id"`
	// ReviewCursor is the poll backstop's high-water mark over forge
	// review IDs. It is deliberately separate from WatchCommentID: review
	// and comment IDs are independent forge sequences, and one combined
	// cursor would silently skip reviews once a larger comment ID landed.
	ReviewCursor int64 `json:"review_cursor"`
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
	// Org is the org the task belongs to. The State Store derives it from
	// the task's identity when the row is written
	// (docs/prds/orgs-and-access.md, "Events and task identity"); a task
	// of a single-operator install belongs to the default org.
	Org org.OrgID `json:"org,omitempty"`
	// BindingID and BindingVersion are stamped when a task was created
	// from a playbook binding dispatch (t2db.4 Phase B). They record
	// provenance: which binding fired this task and at what version, so
	// later edits to the binding cannot silently rewrite history.
	BindingID      string `json:"binding_id"`
	BindingVersion int    `json:"binding_version"`
	// Inputs are the workflow inputs the binding assigned, checked against
	// the workflow's declared inputs at dispatch. They reach the agent as
	// structured data, never as body text.
	Inputs map[string]any `json:"inputs,omitempty"`
	// ReviewPayload is the JSON-encoded review unit (the forge review's
	// actionable comments) the remediate workflow's current run must
	// address. The daemon's reaction consumer injects it before queuing a
	// remediation run, so the run is recoverable from the task record
	// itself rather than only from a prompt (docs/prds/pr-review-remediation.md
	// decision 4). Empty outside a remediation run. RetryCount doubles as
	// the remediation round counter for this same task, bounded by the
	// repo's existing max_retries (decision 5's round cap).
	ReviewPayload string `json:"review_payload"`
	// ParkClass is the producer-recorded answer to "what kind of
	// intervention does this park need?" (taskstate.ParkClass). Set at the
	// park site, never inferred from reason text; needs_human is the
	// default an unclassified park reads as.
	ParkClass string `json:"park_class"`
	// RemediationRounds counts review-triggered remediation rounds,
	// bounded by the repo's max_retries (pr-review-remediation.md
	// decision 5). Deliberately separate from RetryCount, the operator
	// budget: one shared counter made N operator retries eat the
	// review-remediation budget and vice versa.
	RemediationRounds int `json:"remediation_rounds"`
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
// that workflow stages call mid-run. The PostgreSQL store and staterpc.Client
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
	ID      string `json:"id"`
	Name    string `json:"name"`
	Origin  string `json:"origin"`
	Enabled bool   `json:"enabled"`
	// Inputs and Repository are what the workflow declares, so a binding
	// editor can offer the inputs to assign and whether to name a repository.
	Inputs     map[string]InputSpec `json:"inputs,omitempty"`
	Repository RepositoryMode       `json:"repository,omitempty"`
}

// HasRepository reports whether the task works on a repository. A task a
// binding started for a workflow with repository none (or optional, with no
// repository named) has neither owner nor repo and runs in a scratch
// workspace.
func (t Task) HasRepository() bool {
	return t.Owner != "" || t.Repo != ""
}
