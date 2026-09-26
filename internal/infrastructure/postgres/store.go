// Package postgres implements the State Store contract on PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	task "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Store is the PostgreSQL implementation of the State Store. It carries the
// full task/event/identity/status surface behind the storecontract interfaces,
// with the control-plane resource surface embedded from Resources. The pool is
// process-scoped and owned by the composition (internal/app/archied), not by
// the Store, so Close is a no-op.
type Store struct {
	pool *pgxpool.Pool
	*Resources
}

// New returns a Store over pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, Resources: NewResources(pool)}
}

func (s *Store) queries() *postgresdb.Queries {
	return postgresdb.New(s.pool)
}

// guardTransition locks the task's row and checks the status write the caller
// is about to perform: a row whose status is not the expected from is stale
// (ErrStaleTransition), a from->to pair outside the shared transition table is
// refused with ErrIllegalTransition
// (docs/prds/execution-tree-state-machine.md). Staleness is decided first, so
// a caller that is wrong about the row's state gets the stale sentinel even
// when its pair is also unroutable. The row stays locked for the caller's
// transaction, so the checks cannot race the guarded write that follows.
// A missing row is stale, matching what the guarded update alone used to
// return.
func guardTransition(ctx context.Context, q *postgresdb.Queries, taskID int64, from, to string) error {
	status, err := q.LockTaskStatus(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.ErrStaleTransition
	}
	if err != nil {
		return err
	}
	if status != from {
		return storecontract.ErrStaleTransition
	}
	if !taskstate.CanTransition(from, to) {
		return storecontract.ErrIllegalTransition
	}
	return nil
}

// Close is a no-op: this store owns no file handle
// or connection -- the pool belongs to the composition that opened it.
func (s *Store) Close() error { return nil }

// syntheticIssueNumberBase is the seed for a repo's first chat task. The
// COALESCE allocator adds one to the passed fallback, so the fallback here is
// base-1 and the first chat task lands on exactly base: the reserved
// JSON-safe floor that keeps synthetic issue numbers clear of real ones.
const syntheticIssueNumberBase = 1_000_000_000_000_000

// taskFromRow maps the generated task row to the workflow.Task the daemon and
// webui consume.
func taskFromRow(t postgresdb.Task) *workflow.Task {
	// The inputs column is only ever written by EncodeInputs, so a decode
	// failure cannot arise from stored data this package produced.
	inputs, _ := task.DecodeInputs(t.Inputs)
	return &workflow.Task{
		ID:                        t.ID,
		Owner:                     t.Owner,
		Repo:                      t.Repo,
		IssueNumber:               int(t.IssueNumber),
		Title:                     t.Title,
		Body:                      t.Body,
		Labels:                    t.Labels,
		Status:                    t.Status,
		Workflow:                  t.Workflow,
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYAML:    t.WorkflowDefinitionYaml,
		Stage:                     t.Stage,
		Branch:                    t.Branch,
		Plan:                      t.Plan,
		Notes:                     t.Notes,
		PRNumber:                  int(t.PrNumber),
		TokensUsed:                int(t.TokensUsed),
		Iterations:                int(t.Iterations),
		Attempt:                   int(t.Attempt),
		ParkReason:                t.ParkReason,
		RetryCount:                int(t.RetryCount),
		WatchCommentID:            t.WatchCommentID,
		ReviewCursor:              t.ReviewCursor,
		Source:                    t.Source,
		Identity:                  t.Identity,
		Org:                       org.OrgID(t.OrgID),
		BindingID:                 t.BindingID,
		BindingVersion:            int(t.BindingVersion),
		Inputs:                    inputs,
		ReviewPayload:             t.ReviewPayload,
		ParkClass:                 t.ParkClass,
		RemediationRounds:         int(t.RemediationRounds),
		CallParentTaskID:          t.CallParentTaskID,
		CallDepth:                 int(t.CallDepth),
		CreatedAt:                 t.CreatedAt,
		UpdatedAt:                 t.UpdatedAt,
	}
}

// Compile-time checks: *Store satisfies every surface the SQLite store did.
var (
	_ storecontract.TaskStore           = (*Store)(nil)
	_ storecontract.BindingTaskCreator  = (*Store)(nil)
	_ storecontract.ConfigSnapshotStore = (*Store)(nil)
	_ storecontract.ChannelStatusStore  = (*Store)(nil)
	_ storecontract.ApplyStatusStore    = (*Store)(nil)
	_ task.Caller                       = (*Store)(nil)
	_ workflow.Store                    = (*Store)(nil)
)

// EnqueueIssue inserts a new queued task for the issue; returns false if the
// issue is already tracked (the idempotency key is owner/repo/number).
func (s *Store) EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error) {
	n, err := s.queries().EnqueueIssue(ctx, postgresdb.EnqueueIssueParams{
		Owner: owner, Repo: repo, IssueNumber: int64(number),
		Title: title, Body: body, Labels: labels, Identity: identity,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// EnqueueChatTask inserts a queued chat-sourced task and returns the full row.
// The store allocates the synthetic issue number durably so processes sharing
// the database cannot generate the same value.
func (s *Store) EnqueueChatTask(ctx context.Context, owner, repo, title, body, wf, identity string) (*workflow.Task, error) {
	t, err := s.queries().InsertChatTask(ctx, postgresdb.InsertChatTaskParams{
		Owner: owner, Repo: repo, Title: title, Body: body, Workflow: wf, Identity: identity,
		FallbackIssueNumber: syntheticIssueNumberBase - 1,
	})
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// EnqueueBindingTask enqueues a binding-triggered task and stamps its binding
// provenance in a second statement (best-effort, as in the SQLite store).
func (s *Store) EnqueueBindingTask(ctx context.Context, owner, repo, title, body, wf, identity, bindingID string, bindingVersion int, inputs map[string]any) (*workflow.Task, error) {
	encoded, err := task.EncodeInputs(inputs)
	if err != nil {
		return nil, err
	}
	t, err := s.EnqueueChatTask(ctx, owner, repo, title, body, wf, identity)
	if err != nil {
		return nil, err
	}
	if err := s.queries().StampTaskBinding(ctx, postgresdb.StampTaskBindingParams{
		ID: t.ID, BindingID: bindingID, BindingVersion: int64(bindingVersion), Inputs: encoded,
	}); err != nil {
		return nil, fmt.Errorf("store: stamp binding provenance: %w", err)
	}
	t.BindingID = bindingID
	t.BindingVersion = bindingVersion
	t.Inputs = inputs
	return t, nil
}

// ClaimNext atomically moves the oldest queued task to running and returns it;
// nil when the queue is empty.
func (s *Store) ClaimNext(ctx context.Context) (*workflow.Task, error) {
	t, err := s.queries().ClaimNextTask(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// ClaimByIssue atomically claims a queued task by owner/repo/issue_number.
func (s *Store) ClaimByIssue(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	t, err := s.queries().ClaimByIssue(ctx, postgresdb.ClaimByIssueParams{
		Owner: owner, Repo: repo, IssueNumber: int64(number),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// Transition moves a task to a new status and records the audit detail. The
// from status guards the update; a mismatch returns ErrStaleTransition without
// writing an audit row, and a from->to pair outside the shared transition
// table returns ErrIllegalTransition. Transitioning to parked also stores
// detail as ParkReason in the same transaction.
func (s *Store) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := guardTransition(ctx, q, taskID, from, to); err != nil {
		return err
	}
	n, err := q.TransitionTask(ctx, postgresdb.TransitionTaskParams{
		ID: taskID, Status: to, ParkReason: clip(detail, 4000),
		ParkClass: taskstate.ParkNeedsHuman, Status_2: from,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
		TaskID: taskID, FromStatus: from, ToStatus: to, Detail: clip(detail, 4000),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ParkTask is the classified park write: the same guarded running->parked
// transition Transition performs, carrying the park class the site chose.
// The park is only legal from running (the transition table), so a stale or
// off-table from is refused before anything is written.
func (s *Store) ParkTask(ctx context.Context, taskID int64, from, detail, class string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := guardTransition(ctx, q, taskID, from, workflow.StatusParked); err != nil {
		return err
	}
	n, err := q.ParkTask(ctx, postgresdb.ParkTaskParams{
		ID: taskID, ParkReason: clip(detail, 4000),
		ParkClass: taskstate.NormalizeParkClass(class), Status: from,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
		TaskID: taskID, FromStatus: from, ToStatus: workflow.StatusParked, Detail: clip(detail, 4000),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Update persists mutable task fields written by workflows.
func (s *Store) Update(ctx context.Context, t *workflow.Task) error {
	return s.queries().UpdateTask(ctx, postgresdb.UpdateTaskParams{
		ID:                        t.ID,
		Workflow:                  t.Workflow,
		Stage:                     t.Stage,
		Branch:                    t.Branch,
		Plan:                      t.Plan,
		Notes:                     t.Notes,
		PrNumber:                  int64(t.PRNumber),
		TokensUsed:                int64(t.TokensUsed),
		Iterations:                int64(t.Iterations),
		ParkReason:                clip(t.ParkReason, 4000),
		WatchCommentID:            t.WatchCommentID,
		RetryCount:                int64(t.RetryCount),
		RemediationRounds:         int64(t.RemediationRounds),
		ReviewPayload:             t.ReviewPayload,
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYaml:    t.WorkflowDefinitionYAML,
	})
}

// BeginRemediation queues a remediate run carrying the review unit in the same
// guarded write, so a claimed remediation always has its input.
func (s *Store) BeginRemediation(ctx context.Context, taskID int64, payload string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := guardTransition(ctx, q, taskID, workflow.StatusPROpen, workflow.StatusQueued); err != nil {
		return err
	}
	n, err := q.BeginRemediationTask(ctx, postgresdb.BeginRemediationTaskParams{
		ID: taskID, ReviewPayload: clip(payload, 4000),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
		TaskID: taskID, FromStatus: workflow.StatusPROpen, ToStatus: workflow.StatusQueued,
		Detail: "review reaction queued remediation",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateReviewPayload replaces a queued remediation's review unit.
func (s *Store) UpdateReviewPayload(ctx context.Context, taskID int64, payload string) error {
	n, err := s.queries().UpdateReviewPayloadTask(ctx, postgresdb.UpdateReviewPayloadTaskParams{
		ID: taskID, ReviewPayload: clip(payload, 4000),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	return nil
}

// SetReviewCursors persists the poll backstop's per-task review and comment
// high-water marks, guarded on pr_open.
func (s *Store) SetReviewCursors(ctx context.Context, taskID, reviewCursor, commentCursor int64) error {
	n, err := s.queries().SetReviewCursors(ctx, postgresdb.SetReviewCursorsParams{
		ID: taskID, ReviewCursor: reviewCursor, WatchCommentID: commentCursor,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	return nil
}

// Requeue puts a task back on the queue; an empty workflow keeps the task's
// current workflow. The from->queued pair must be routed by the transition
// table: requeueing out of a terminal status is refused, not rewritten.
func (s *Store) Requeue(ctx context.Context, taskID int64, fromStatus, wf string) error {
	return s.requeue(ctx, taskID, fromStatus, wf, "requeued "+wf, func(q *postgresdb.Queries) (int64, error) {
		return q.RequeueTask(ctx, postgresdb.RequeueTaskParams{
			ID: taskID, Workflow: wf, FromStatus: fromStatus,
		})
	})
}

// RetryTask requeues a task and increments retry_count in the same guarded
// transaction, under the same table check Requeue applies.
func (s *Store) RetryTask(ctx context.Context, taskID int64, fromStatus, wf string) error {
	return s.requeue(ctx, taskID, fromStatus, wf, "retried "+wf, func(q *postgresdb.Queries) (int64, error) {
		return q.RetryTask(ctx, postgresdb.RetryTaskParams{
			ID: taskID, Workflow: wf, FromStatus: fromStatus,
		})
	})
}

// requeue carries the guarded requeue write Requeue and RetryTask share: the
// transition-table check, the update guarded on the from status, and one audit
// row. update is the query that differs between the two callers; detail is the
// audit line it records.
func (s *Store) requeue(ctx context.Context, taskID int64, fromStatus, wf, detail string, update func(*postgresdb.Queries) (int64, error)) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := guardTransition(ctx, q, taskID, fromStatus, workflow.StatusQueued); err != nil {
		return err
	}
	n, err := update(q)
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
		TaskID: taskID, FromStatus: fromStatus, ToStatus: workflow.StatusQueued, Detail: detail,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ArchiveTask removes one terminal task from the active task board and returns
// the id of the audit event written alongside the delete.
func (s *Store) ArchiveTask(ctx context.Context, taskID int64, fromStatus string, audit events.Event) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	eventID, err := insertEventQ(ctx, q, audit)
	if err != nil {
		return 0, err
	}
	n, err := q.ArchiveTaskDelete(ctx, postgresdb.ArchiveTaskDeleteParams{
		ID: taskID, Status: fromStatus,
	})
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, storecontract.ErrStaleTransition
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return eventID, nil
}

// RecoverStale re-queues tasks left running by a crashed daemon. The write is
// the table's crash-recovery edge (running->queued), pinned in SQL and pinned
// to the table by TestSQLPinnedTransitionsAreTableLegal.
func (s *Store) RecoverStale(ctx context.Context) (int64, error) {
	return s.queries().RecoverStaleTasks(ctx)
}

// OpenPRs returns tasks whose PR state should be reconciled with the forge,
// carrying the attempt and the review/comment cursors the reconcile loop and
// poll backstop read.
func (s *Store) OpenPRs(ctx context.Context) ([]workflow.Task, error) {
	rows, err := s.queries().ListOpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]workflow.Task, 0, len(rows))
	for _, r := range rows {
		tasks = append(tasks, workflow.Task{
			ID: r.ID, Owner: r.Owner, Repo: r.Repo, IssueNumber: int(r.IssueNumber),
			PRNumber: int(r.PrNumber), Status: r.Status, Source: r.Source,
			Identity: r.Identity, Attempt: int(r.Attempt),
			ReviewCursor: r.ReviewCursor, WatchCommentID: r.WatchCommentID,
		})
	}
	return tasks, nil
}

// ClearTerminalTasks deletes tasks whose status is terminal.
func (s *Store) ClearTerminalTasks(ctx context.Context) (int64, error) {
	return s.queries().ClearTerminalTasks(ctx)
}

// Tasks returns all tasks, newest first (dashboard listing).
func (s *Store) Tasks(ctx context.Context, limit int) ([]workflow.Task, error) {
	rows, err := s.queries().ListTaskSummaries(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	tasks := make([]workflow.Task, 0, len(rows))
	for _, r := range rows {
		tasks = append(tasks, workflow.Task{
			ID: r.ID, Owner: r.Owner, Repo: r.Repo, IssueNumber: int(r.IssueNumber),
			Title: r.Title, Status: r.Status, Workflow: r.Workflow, Stage: r.Stage,
			PRNumber: int(r.PrNumber), TokensUsed: int(r.TokensUsed), Iterations: int(r.Iterations),
			Attempt: int(r.Attempt), ParkReason: r.ParkReason, RetryCount: int(r.RetryCount),
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Plan: r.Plan, Source: r.Source,
			Identity: r.Identity, BindingID: r.BindingID, BindingVersion: int(r.BindingVersion),
		})
	}
	return tasks, nil
}

// StatusCounts returns task counts by status.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.queries().CountTasksByStatus(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, r := range rows {
		counts[r.Status] = int(r.Count)
	}
	return counts, nil
}

// TaskByIssue returns the task tracking an issue, or nil.
func (s *Store) TaskByIssue(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	t, err := s.queries().TaskByIssue(ctx, postgresdb.TaskByIssueParams{
		Owner: owner, Repo: repo, IssueNumber: int64(number),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// OpenTaskByPR returns the live task that owns the given pull request, or nil.
func (s *Store) OpenTaskByPR(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	t, err := s.queries().TaskByPR(ctx, postgresdb.TaskByPRParams{
		Owner: owner, Repo: repo, PrNumber: int64(number), Status: workflow.StatusPROpen,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// TaskByID returns the task with the given database ID, or nil.
func (s *Store) TaskByID(ctx context.Context, taskID int64) (*workflow.Task, error) {
	t, err := s.queries().TaskByID(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// StartCall enqueues the callee of callerTaskID's workflow.call step
// (docs/prds/workflow-calls.md). The store owns the table, so it re-checks
// both runtime invariants the engine checks: the caller must be running and
// the call must not pass workflow.MaxCallDepth. The callee inherits the
// caller's org, workspace, identity, owner and repo, and takes a fresh
// synthetic issue number, the same allocator chat tasks use.
func (s *Store) StartCall(ctx context.Context, callerTaskID int64, wf string, inputs map[string]any) (*workflow.Task, error) {
	encoded, err := task.EncodeInputs(inputs)
	if err != nil {
		return nil, err
	}
	callee, err := s.queries().EnqueueCallTask(ctx, postgresdb.EnqueueCallTaskParams{
		ID:                  callerTaskID,
		FallbackIssueNumber: syntheticIssueNumberBase - 1,
		Title:               fmt.Sprintf("workflow.call %s from task %d", wf, callerTaskID),
		Body:                fmt.Sprintf("Started by a workflow.call step from task %d (workflow %q); the call's inputs travel with the task.", callerTaskID, wf),
		Workflow:            wf,
		Inputs:              encoded,
		MaxDepth:            int32(workflow.MaxCallDepth),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The WITH reads the caller FOR UPDATE before inserting, so "no
		// rows" means the caller is missing, not running, or past the depth
		// limit. Name which, off a plain read.
		return nil, s.callRefusal(ctx, callerTaskID)
	}
	if err != nil {
		return nil, err
	}
	return taskFromRow(callee), nil
}

// callRefusal names why EnqueueCallTask inserted nothing. The three refusals
// are distinct failures an operator reads differently: a missing caller is a
// problem, a caller that is not running is a stale retry, and a depth
// refusal is the limit doing its job. The two behavioural ones carry the
// caller's detail in the log and the sentinel across the wire.
func (s *Store) callRefusal(ctx context.Context, callerTaskID int64) error {
	caller, err := s.queries().TaskByID(ctx, callerTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: workflow.call caller task %d does not exist: %w", callerTaskID, storecontract.ErrCallNotYours)
	}
	if err != nil {
		return err
	}
	if caller.Status != "running" {
		return fmt.Errorf("store: workflow.call caller task %d is %s: %w", callerTaskID, caller.Status, storecontract.ErrCallCallerNotRunning)
	}
	return fmt.Errorf("store: workflow.call from task %d at depth %d: %w", callerTaskID, caller.CallDepth, storecontract.ErrCallDepthExceeded)
}

// CallStatus reads one call's callee for a waiting caller: its status and
// latest transition detail. The parent check is the row check the wire grant
// cannot do: a caller reads only the tasks it started.
func (s *Store) CallStatus(ctx context.Context, callerTaskID, callTaskID int64) (string, string, error) {
	callee, err := s.queries().TaskByID(ctx, callTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", storecontract.ErrCallNotYours
	}
	if err != nil {
		return "", "", err
	}
	if callee.CallParentTaskID != callerTaskID {
		return "", "", storecontract.ErrCallNotYours
	}
	// A callee that has not moved yet has no transition row; its queue
	// status is the whole answer, and the empty detail says so.
	detail, err := s.queries().CallStatusDetail(ctx, callTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return callee.Status, "", nil
	}
	if err != nil {
		return "", "", err
	}
	return callee.Status, detail, nil
}
