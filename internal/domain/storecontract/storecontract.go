// Package storecontract defines the State Store interfaces its consumers use,
// without an implementation.
package storecontract

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
)

// TaskStore is the full store surface the daemon needs.
// Consumers depend on TaskStore, never on a concrete type.
type TaskStore interface {
	TaskLifecycle
	TaskParker
	TaskEvents
	TaskQueries
	TaskArchiver
	TaskRetryer
	ReviewGateResponder
	RemediationStarter
}

// TaskLifecycle manages the core task state machine and enqueuing.
// Binding-triggered task creation lives on BindingTaskCreator, not here,
// so the lifecycle surface stays narrow and non-binding consumers (forge
// poll, chat spawn, drain loop) do not acquire a binding-specific shape.
type TaskLifecycle interface {
	EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error)
	EnqueueChatTask(ctx context.Context, owner, repo, title, body, wf, identity string, inputs map[string]any) (*task.Task, error)
	ClaimNext(ctx context.Context) (*task.Task, error)
	ClaimByIssue(ctx context.Context, owner, repo string, number int) (*task.Task, error)
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
	Update(ctx context.Context, t *task.Task) error
	Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error
	RecoverStale(ctx context.Context) (int64, error)
}

// TaskParker parks a task with a park class. Unknown classes become
// needs_human.
type TaskParker interface {
	ParkTask(ctx context.Context, taskID int64, from, detail, class string) error
}

// StepRecorder records a running execution's steps.
type StepRecorder interface {
	StartStep(ctx context.Context, s task.StepStart) (int64, events.Event, error)
	FinishStep(ctx context.Context, s task.StepFinish) (events.Event, error)
}

// ExecutionCanceller cancels an execution and its current attempt's open
// steps in one transaction.
type ExecutionCanceller interface {
	CancelExecution(ctx context.Context, taskID int64, reason, to string) ([]int64, error)
}

// StepReader lists one execution's recorded steps.
type StepReader interface {
	ListSteps(ctx context.Context, executionID int64, attempt int) ([]task.StepExecution, error)
}

// HarnessSecretStore stores one org's OAuth token set per credential binding
// service, encrypted at rest.
type HarnessSecretStore interface {
	GetHarnessSecret(ctx context.Context, org, service string) (harnesssecret.Secret, error)
	PutHarnessSecret(ctx context.Context, s harnesssecret.Secret) error
}

// TaskArchiver removes one terminal task's local record with an optimistic
// status guard. It is separate from the already broad lifecycle contract so
// consumers that only run tasks do not acquire an operator-only capability.
type TaskArchiver interface {
	ArchiveTask(ctx context.Context, taskID int64, fromStatus string, audit events.Event) (eventID int64, err error)
}

// TaskRetryer atomically requeues recoverable work and accounts for the new
// attempt so a partial write cannot evade the retry cap. retryMode is the
// operator's worktree choice for the next dispatch (taskstate.RetryMode),
// persisted on the row so the daemon reads it instead of inferring it.
type TaskRetryer interface {
	RetryTask(ctx context.Context, taskID int64, fromStatus, workflow, retryMode string) error
}

// ReviewGateResponder records the operator's review-gate answer. A re-review
// increments rereview_rounds and requeues; one past the cap returns
// ErrRereviewCapReached and writes nothing.
type ReviewGateResponder interface {
	RespondReviewGate(ctx context.Context, taskID int64, fromStatus, gate string, rereview bool, maxRounds int) error
}

// RemediationStarter queues a review-triggered remediation for an Archie-owned
// open-PR task: one guarded transition that carries the JSON-encoded review
// unit in the same write, so a claimed remediation always has its input.
// UpdateReviewPayload appends late-arriving comments to a still-queued unit.
type RemediationStarter interface {
	BeginRemediation(ctx context.Context, taskID int64, payload string) error
	UpdateReviewPayload(ctx context.Context, taskID int64, payload string) error
	// SetReviewCursors persists the poll backstop's per-task review and
	// comment high-water marks (pr-review-remediation.md decision 2). The
	// pr_open guard is part of the contract: cursors only move while no
	// remediation run owns the task.
	SetReviewCursors(ctx context.Context, taskID, reviewCursor, commentCursor int64) error
}

// TaskQueries groups read-only task accessors.
type TaskQueries interface {
	TaskByIssue(ctx context.Context, owner, repo string, number int) (*task.Task, error)
	OpenTaskByPR(ctx context.Context, owner, repo string, number int) (*task.Task, error)
	TaskByID(ctx context.Context, taskID int64) (*task.Task, error)
	OpenPRs(ctx context.Context) ([]task.Task, error)
	ClearTerminalTasks(ctx context.Context) (int64, error)
	Tasks(ctx context.Context, limit int) ([]task.Task, error)
	StatusCounts(ctx context.Context) (map[string]int, error)
}

// TaskEvents groups observability and lifecycle methods.
type TaskEvents interface {
	InsertEvent(ctx context.Context, e events.Event) (int64, error)
	EventsSince(ctx context.Context, cursor string, limit int) ([]events.Event, error)
	TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error)
	WorkflowStats(ctx context.Context) ([]WorkflowStat, error)
	StageStats(ctx context.Context) ([]StageStat, error)
	TokensByDay(ctx context.Context, days int) ([]DayTokens, error)
	Close() error
}

// CaptureStore persists inbound webhook captures.
type CaptureStore interface {
	InsertCapture(ctx context.Context, c CapturedEvent, retention time.Duration, maxEvents int) (string, error)
	ListCaptures(ctx context.Context, limit int) ([]CapturedEvent, error)
}

// ConfigSnapshotStore holds the published configuration view.
type ConfigSnapshotStore interface {
	PutConfigSnapshot(ctx context.Context, snapshot ConfigSnapshot) error
	ConfigSnapshot(ctx context.Context) (ConfigSnapshot, bool, error)
}

// ChannelStatusStore holds channel runtime state published by the process
// hosting the channels.
type ChannelStatusStore interface {
	// PutChannelStatus records the reporting process's whole set: a channel it no
	// longer reports is removed rather than left behind, because a stale "running"
	// row is a lie the dashboard would show indefinitely.
	PutChannelStatus(ctx context.Context, channels []ChannelStatus) error
	ChannelStatus(ctx context.Context) ([]ChannelStatus, error)
}

// ApplyStatusStore holds which version of each control-plane resource each
// process is running. Same split as ConfigSnapshotStore and for the same
// reason: the writers are the processes that apply a resource, the reader is
// the UI, and a task-scoped credential must reach neither.
type ApplyStatusStore interface {
	PutApplyStatus(ctx context.Context, status ApplyStatus) error
	ListApplyStatus(ctx context.Context) ([]ApplyStatus, error)
}

// MappingStore persists payload field mappings. Deliberately separate from
// TaskStore and CaptureStore for the same reason those are split: the
// dashboard's mapping editor should only acquire the mapping surface, not the
// full task or capture APIs. See
type MappingStore interface {
	InsertMapping(ctx context.Context, m mapping.Mapping) (string, error)
	GetMapping(ctx context.Context, id string) (*mapping.Mapping, error)
	ListMappings(ctx context.Context) ([]mapping.Mapping, error)
	UpdateMapping(ctx context.Context, m mapping.Mapping) error
	DeleteMapping(ctx context.Context, id string) error
}

// MappingMatchRecorder counts the events each mapping resolved, which the
// store reports as Mapping.MatchCount and LastMatchedAt. Recording the same
// (mapping, capture) twice counts once.
type MappingMatchRecorder interface {
	RecordMappingMatch(ctx context.Context, mappingID, captureID string) error
}

// EventTypeStore persists event types. Insert and Update refuse a type whose
// rule overlaps another on the same source with eventtype.ErrOverlap, and a
// malformed one with eventtype.ErrInvalid. Update rewrites the name and rule;
// the schema is the one the type was created with.
type EventTypeStore interface {
	InsertEventType(ctx context.Context, t eventtype.EventType) (string, error)
	UpdateEventType(ctx context.Context, t eventtype.EventType) error
	DeleteEventType(ctx context.Context, id string) error
	ListEventTypes(ctx context.Context) ([]eventtype.EventType, error)
}

// BindingStore persists bindings and their draft -> pending_approval ->
// armed states.
type BindingStore interface {
	InsertBinding(ctx context.Context, b binding.Binding) (string, error)
	GetBinding(ctx context.Context, id string) (*binding.Binding, error)
	ListBindings(ctx context.Context) ([]binding.Binding, error)
	UpdateBinding(ctx context.Context, b binding.Binding) error
	DeleteBinding(ctx context.Context, id string) error
	ApproveBinding(ctx context.Context, id string) error
}

// SourceStore persists capture sources keyed by path. Secrets are encrypted
// at rest; signing transitions use an expected-from guard.
type SourceStore interface {
	InsertSource(ctx context.Context, s source.Source) error
	// GetSource returns (nil, nil) for an unknown path.
	GetSource(ctx context.Context, path string) (*source.Source, error)
	ListSources(ctx context.Context) ([]source.Source, error)
	SetSourceSigning(ctx context.Context, path string, from, to source.Signing) error
	SetSourceSecret(ctx context.Context, path, secret string) error
}

// BindingDispatcher looks up armed bindings, lists undispatched captures and
// records dispatches.
type BindingDispatcher interface {
	ArmedBindingsForSource(ctx context.Context, source string) ([]binding.Binding, error)
	RecordDispatch(
		ctx context.Context,
		bindingID string,
		bindingVersion int64,
		captureID string,
		// taskID addresses the SQLite-owned task tables, which are not
		// migrating: it stays an integer on purpose. The binding dispatch
		// loop claims before it enqueues, so it records 0.
		taskID int64,
	) error
	ListUndispatchedCaptures(ctx context.Context, sources []string, limit int) ([]CapturedEvent, error)
}

// BindingTaskCreator is the single-method consumer-facing surface for
// enqueueing a task triggered by a binding. Splitting it off TaskLifecycle
// keeps the lifecycle surface narrow (8 methods, the interfacebloat limit)
// and keeps the binding-specific shape on the binding interfaces.
type BindingTaskCreator interface {
	EnqueueBindingTask(ctx context.Context, owner, repo, title, body, wf, identity, bindingID string, bindingVersion int, inputs map[string]any) (*task.Task, error)
}

// WorkflowCaller starts a workflow.call callee and reads it back.
type WorkflowCaller = task.Caller

// PlaybookDispatcher records a playbook action dispatch before its side
// effect runs, returning ErrAlreadyDispatched for a duplicate.
// DeletePlaybookDispatches deletes one playbook's records.
type PlaybookDispatcher interface {
	RecordPlaybookDispatch(ctx context.Context, playbookID, playbookVersion, eventID, actionID string) error
	DeletePlaybookDispatches(ctx context.Context, playbookID string) error
}

// TaskLogStore reads one task attempt's log. Found=false means no log;
// ErrTaskLogsUnavailable means this process cannot read logs.
type TaskLogStore interface {
	// TaskLog returns a page of one attempt's decoded log. attempt 0 selects
	// the task's current attempt.
	TaskLog(ctx context.Context, taskID int64, attempt int, q logging.Query) (logging.TaskLogPage, error)
	// TaskLogContent writes one attempt's log verbatim to w, for a download.
	// found is false, with a nil error, when the attempt has no log file.
	TaskLogContent(ctx context.Context, taskID int64, attempt int, w io.Writer) (found bool, err error)
}

// CapturedEvent is one unbound inbound webhook capture: no workflow binding,
// no forge/task association -- just what arrived, from where, and when.
type CapturedEvent struct {
	ID          string    `json:"id"`
	ReceivedAt  time.Time `json:"received_at"`
	Source      string    `json:"source"`
	RemoteAddr  string    `json:"remote_addr"`
	ContentType string    `json:"content_type"`
	// Headers and Body are redacted (webhookguard.RedactPayload) before
	// they ever reach InsertCapture; the store persists what it is given.
	Headers       string `json:"headers"`
	Body          string `json:"body"`
	Authenticated bool   `json:"authenticated"`
	// EventType is the ID of the event type the capture was identified as
	// when it arrived, or empty when it matched none. An unidentified capture
	// is never dispatched.
	EventType string `json:"event_type"`
	// Unsigned marks an event that arrived on an approved unsigned source.
	// It dispatches without Authenticated and is flagged wherever it shows.
	Unsigned bool `json:"unsigned"`
}

// Dispatchable reports whether a binding may start a task from this event:
// a valid signature, or a source approved to take unsigned events.
func (c CapturedEvent) Dispatchable() bool { return c.Authenticated || c.Unsigned }

// ConfigSnapshot is the published, secret-free configuration view. Schema
// names Document's shape.
type ConfigSnapshot struct {
	Schema      string
	Document    []byte
	PublishedAt time.Time
}

// ChannelStatus is one channel's runtime state as the process hosting it
// reports it. ReloadSupported is a capability declaration rather than a request:
// asking for a reload travels on its own surface, because a transient request and
// a durable state do not share a lifetime.
type ChannelStatus struct {
	ID              string
	Name            string
	State           string
	Detail          string
	Configured      bool
	ReloadSupported bool
	ObservedAt      time.Time
}

// ApplyStatus is one process's report on one resource kind. On error,
// AppliedVersion stays at the last applied version. ReportedAt is
// re-stamped periodically.
type ApplyStatus struct {
	Process        string    `json:"process"`
	Kind           string    `json:"kind"`
	AppliedVersion int64     `json:"applied_version"`
	Error          string    `json:"error,omitempty"`
	ReportedAt     time.Time `json:"reported_at"`
}

// WorkflowStat is one row of the per-workflow metrics table.
type WorkflowStat struct {
	Workflow   string  `json:"workflow"`
	Runs       int     `json:"runs"`
	Merged     int     `json:"merged"`
	Completed  int     `json:"completed"`
	PROpen     int     `json:"pr_open"`
	Parked     int     `json:"parked"`
	AvgTokens  int     `json:"avg_tokens"`
	AvgSteps   float64 `json:"avg_steps"`
	TotalToken int     `json:"total_tokens"`
}

// StageStat is average stage duration and failure counts per stage.
type StageStat struct {
	Workflow string `json:"workflow"`
	Stage    string `json:"stage"`
	Runs     int    `json:"runs"`
	AvgMs    int    `json:"avg_ms"`
	Errors   int    `json:"errors"`
}

// DayTokens is token spend per UTC day.
type DayTokens struct {
	Day    string `json:"day"`
	Tokens int    `json:"tokens"`
}

// Sentinel errors, part of the wire contract: staterpc's
// mapError/unmapError match these by (code, canonical message) across the
// gRPC boundary, so their message strings are load-bearing and must never
// change.
var (
	// ErrStaleTransition is returned when a task transition's expected
	// from status does not match the task's current status.
	ErrStaleTransition = errors.New("store: stale transition: task status does not match expected from status")
	// ErrIllegalTransition is returned when the transition table does not allow
	// the status pair.
	ErrIllegalTransition = errors.New("store: illegal transition: status pair is not in the transition table")
	// ErrInvalidStep is returned when a step-execution request names a kind the
	// store does not record or a step whose identifying fields do not resolve.
	// Distinct from the transition sentinels, which cover a row's state.
	ErrInvalidStep = errors.New("store: invalid step execution request")
	// ErrBindingNotFound is returned when a binding ID does not exist.
	ErrBindingNotFound = errors.New("store: binding not found")
	// ErrBindingOverlap is returned when a binding's matcher overlaps an
	// existing binding for the same source.
	ErrBindingOverlap = errors.New("store: binding overlaps existing binding for source")
	// ErrBindingTransition is returned when a binding state transition is
	// rejected by the draft -> pending_approval -> armed machine.
	ErrBindingTransition = errors.New("store: binding state transition rejected")
	// ErrAlreadyDispatched is returned when a dispatch ledger already holds the
	// key.
	ErrAlreadyDispatched = errors.New("store: binding already dispatched for capture")
	// ErrCallNotYours is returned when a workflow.call status read names a
	// task that is not a callee of the calling task, or the caller itself
	// does not exist: a caller reads only the tasks it started
	ErrCallNotYours = errors.New("store: task is not a callee of the calling task")
	// ErrCallCallerNotRunning is returned when a workflow.call enqueue names
	// a caller that is not running: a stale retry, or a bug in the caller.
	ErrCallCallerNotRunning = errors.New("store: workflow.call caller is not running")
	// ErrCallDepthExceeded is returned when a workflow.call enqueue would
	// pass workflow.MaxCallDepth. The engine refuses the same call first;
	// the store re-checks because it owns the table.
	ErrCallDepthExceeded = errors.New("store: workflow.call would pass the depth limit")
	// ErrMappingNotFound is returned when a mapping ID does not exist.
	ErrMappingNotFound = errors.New("store: mapping not found")
	// ErrEventTypeNotFound is returned when an event type ID does not exist.
	ErrEventTypeNotFound = errors.New("store: event type not found")
	// ErrSourceNotFound is returned when a source path does not exist.
	ErrSourceNotFound = errors.New("store: source not found")
	// ErrSourcePathTaken is returned when a new source's path is in use.
	ErrSourcePathTaken = errors.New("store: source path already taken")
	// ErrSourceSigningStale is returned when a signing write's expected
	// from state does not match the stored one.
	ErrSourceSigningStale = errors.New("store: source signing does not match expected state")
	// ErrHarnessSecretNotFound is returned when no OAuth token set is
	// stored for an org/service pair -- the setup terminal has not
	// captured one yet.
	ErrHarnessSecretNotFound = errors.New("store: harness secret not found")
	// ErrRereviewCapReached is returned when a review gate re-review would
	// round past prreview.MaxRereviewRounds. The task stays waiting and its
	// gate row is unchanged. The dashboard answers it 409, like every other
	// conflict.
	ErrRereviewCapReached = errors.New("store: re-review cap reached")

	// ErrResourceNotFound is returned when a control-plane resource kind has
	// no stored document. It is in-process only (not on the gRPC wire): the
	// control plane runs in the daemon against the local store.
	ErrResourceNotFound = errors.New("resource not found")
	// ErrResourceVersionConflict is returned when a resource write carries an
	// expected version that does not match the stored revision.
	ErrResourceVersionConflict = errors.New("resource version conflict")
)

// Resource is one control-plane resource document: the stored value for an
// org's kind, its revision, and the audit record written with the last write.
// Resource and ResourceWrite live here so the UI-side consumers can reference
// them without linking the PostgreSQL implementation.
type Resource struct {
	OrgID           string
	Kind            string
	Value           []byte
	Version         int64
	Actor           string
	Source          string
	RequestID       string
	ExpectedVersion int64
	CurrentVersion  int64
	At              time.Time
}

// AuditTableResources is the sys_audit table name for control-plane resources,
// keyed by ResourceAuditKey.
const AuditTableResources = "resources"

// DefaultOrgID is the org every resource written before orgs existed belongs
// to, and the org a request that names none acts in.
const DefaultOrgID = "default"

// ResourceAuditKey is the sys_audit record key of one org's resource kind. The
// default org keeps the bare kind, so audit rows written before resources were
// per-org still resolve.
func ResourceAuditKey(orgID, kind string) string {
	if orgID == DefaultOrgID {
		return kind
	}
	return orgID + "/" + kind
}

// AuditEntry is one changed field of one record, as sys_audit holds it. Any
// store table may write entries; Table and RecordKey locate the record.
// OldValue and NewValue are JSON; an absent side is empty.
type AuditEntry struct {
	ID        int64
	Table     string
	RecordKey string
	Field     string
	OldValue  []byte
	NewValue  []byte
	Version   int64
	Actor     string
	Source    string
	RequestID string
	At        time.Time
}

// ResourceWrite is a control-plane resource write request. ExpectedVersion is
// the optimistic-concurrency guard: a write whose expectation does not match
// the stored revision is refused with ErrResourceVersionConflict.
type ResourceWrite struct {
	OrgID           string
	Kind            string
	Value           []byte
	Actor           string
	Source          string
	RequestID       string
	ExpectedVersion int64
	At              time.Time
}
