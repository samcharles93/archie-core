// Package postgres implements the State Store contract on PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	task "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Store is the PostgreSQL State Store. The pool belongs to the caller, so
// Close is a no-op.
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

// guardTransition locks the task row and checks the transition: a status
// other than from is ErrStaleTransition, a disallowed pair is
// ErrIllegalTransition. A missing row is stale.
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

// structuredPayloadBytes bounds structured payloads. Oversized outputs are
// refused; review payloads are clipped.
const structuredPayloadBytes = 4000

// taskFromRow maps the generated task row to the workflow.Task the daemon and
// webui consume.
func taskFromRow(t postgresdb.Task) *workflow.Task {
	// The inputs and outputs columns are only ever written by EncodeInputs and
	// EncodeOutputs, so a decode failure cannot arise from stored data this
	// package produced.
	inputs, _ := task.DecodeInputs(t.Inputs)
	outputs, _ := task.DecodeOutputs(t.Outputs)
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
		Branch:                    t.Branch,
		Plan:                      t.Plan,
		Notes:                     t.Notes,
		PRNumber:                  int(t.PrNumber),
		TokensUsed:                int(t.TokensUsed),
		Iterations:                int(t.Iterations),
		Attempt:                   int(t.Attempt),
		ParkReason:                t.ParkReason,
		RetryCount:                int(t.RetryCount),
		RetryMode:                 t.RetryMode,
		ResumeFrom:                t.ResumeFrom,
		ResumeResults:             t.ResumeResults,
		WatchCommentID:            t.WatchCommentID,
		ReviewCursor:              t.ReviewCursor,
		Source:                    t.Source,
		Identity:                  t.Identity,
		Org:                       org.OrgID(t.OrgID),
		BindingID:                 t.BindingID,
		BindingVersion:            int(t.BindingVersion),
		Inputs:                    inputs,
		Outputs:                   outputs,
		ReviewPayload:             t.ReviewPayload,
		ReviewGate:                t.ReviewGate,
		RereviewRounds:            int(t.RereviewRounds),
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
	_ storecontract.StepRecorder        = (*Store)(nil)
	_ storecontract.ExecutionCanceller  = (*Store)(nil)
	_ storecontract.BindingTaskCreator  = (*Store)(nil)
	_ storecontract.ConfigSnapshotStore = (*Store)(nil)
	_ storecontract.ChannelStatusStore  = (*Store)(nil)
	_ storecontract.ApplyStatusStore    = (*Store)(nil)
	_ storecontract.PresenceStore       = (*Store)(nil)
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

// EnqueueChatTask inserts a queued chat task with a store-allocated synthetic
// issue number. origin names the conversation that created it, if any.
func (s *Store) EnqueueChatTask(ctx context.Context, owner, repo, title, body, wf, identity, origin string, inputs map[string]any) (*workflow.Task, error) {
	encoded, err := task.EncodeInputs(inputs)
	if err != nil {
		return nil, err
	}
	t, err := s.queries().InsertChatTask(ctx, postgresdb.InsertChatTaskParams{
		Owner: owner, Repo: repo, Title: title, Body: body, Workflow: wf, Identity: identity,
		Inputs:              encoded,
		Origin:              origin,
		FallbackIssueNumber: syntheticIssueNumberBase - 1,
	})
	if err != nil {
		return nil, err
	}
	return taskFromRow(t), nil
}

// EnqueueBindingTask enqueues a binding-triggered task and stamps its binding
// provenance in a second statement (best-effort, as in the SQLite store). The
// inputs travel with the insert, so a failed stamp leaves a task whose inputs
// are already right and whose provenance is absent, never the reverse.
func (s *Store) EnqueueBindingTask(ctx context.Context, owner, repo, title, body, wf, identity, bindingID string, bindingVersion int, inputs map[string]any) (*workflow.Task, error) {
	t, err := s.EnqueueChatTask(ctx, owner, repo, title, body, wf, identity, "", inputs)
	if err != nil {
		return nil, err
	}
	if err := s.queries().StampTaskBinding(ctx, postgresdb.StampTaskBindingParams{
		ID: t.ID, BindingID: bindingID, BindingVersion: int64(bindingVersion),
	}); err != nil {
		return nil, fmt.Errorf("store: stamp binding provenance: %w", err)
	}
	t.BindingID = bindingID
	t.BindingVersion = bindingVersion
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

// Transition moves a task from `from` to `to` with an audit row. Parking
// also stores detail as ParkReason.
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
	// A remediate run that finished with reviews still waiting behind it
	// hands the task straight back to the queue, oldest first. The run itself
	// cannot know they are there: they arrived while it held the task, and
	// only the state store owns both halves of the queue.
	if to == workflow.StatusPROpen {
		promoted, err := q.PromotePendingReview(ctx, taskID)
		if err != nil {
			return err
		}
		if promoted > 0 {
			if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
				TaskID: taskID, FromStatus: workflow.StatusPROpen, ToStatus: workflow.StatusQueued,
				Detail: "queued the next review",
			}); err != nil {
				return err
			}
		}
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
	// Oversized outputs are refused, not clipped.
	outputs, err := task.EncodeOutputs(t.Outputs)
	if err != nil {
		return fmt.Errorf("store: encode task %d outputs: %w", t.ID, err)
	}
	if len(outputs) > structuredPayloadBytes {
		return fmt.Errorf("store: task %d outputs are %d bytes, past the %d-byte structured-payload bound; refuse rather than clip", t.ID, len(outputs), structuredPayloadBytes)
	}
	return s.queries().UpdateTask(ctx, postgresdb.UpdateTaskParams{
		ID:                        t.ID,
		Workflow:                  t.Workflow,
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
		ReviewGate:                t.ReviewGate,
		RereviewRounds:            int64(t.RereviewRounds),
		RetryMode:                 t.RetryMode,
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYaml:    t.WorkflowDefinitionYAML,
		Outputs:                   outputs,
	})
}

// BeginRemediation queues a remediate run carrying the review unit in the same
// guarded write, so a claimed remediation always has its input. When a
// remediation already owns the task, the unit is queued behind the run in
// flight instead of refused: a review that arrives mid-remediation is
// remediated after it, never dropped.
func (s *Store) BeginRemediation(ctx context.Context, taskID int64, payload string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := guardTransition(ctx, q, taskID, workflow.StatusPROpen, workflow.StatusQueued); err != nil {
		if !errors.Is(err, storecontract.ErrStaleTransition) {
			return err
		}
		// A remediation already owns the task. Runs against one task are
		// serialised, so queue the review behind the current one rather than
		// dropping it; Transition promotes it when that run finishes.
		n, err := q.AppendPendingReview(ctx, postgresdb.AppendPendingReviewParams{
			ID: taskID, Column2: clip(payload, 4000),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return storecontract.ErrStaleTransition
		}
		return tx.Commit(ctx)
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

// UpdateReviewPayload merges payload's comments into the not-yet-claimed unit
// for the same review: the active unit while the remediation is still queued,
// or a unit waiting in pending_reviews behind a running one. A unit already
// claimed is frozen, so a comment for it is ErrStaleTransition.
func (s *Store) UpdateReviewPayload(ctx context.Context, taskID int64, payload string) error {
	incoming, err := workflow.DecodeReviewUnit(payload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)
	row, err := q.LockReviewUnits(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.ErrStaleTransition
	}
	if err != nil {
		return err
	}
	if row.Workflow != "remediate" {
		return storecontract.ErrStaleTransition
	}
	var pending []string
	if err := json.Unmarshal([]byte(row.PendingReviews), &pending); err != nil {
		return fmt.Errorf("decode pending reviews: %w", err)
	}
	active := row.ReviewPayload
	merged := false
	if row.Status == workflow.StatusQueued {
		active, merged = mergeReviewComments(active, incoming)
	}
	for i := range pending {
		if merged {
			break
		}
		pending[i], merged = mergeReviewComments(pending[i], incoming)
	}
	if !merged {
		return storecontract.ErrStaleTransition
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if err := q.SetReviewUnits(ctx, postgresdb.SetReviewUnitsParams{
		ID: taskID, ReviewPayload: clip(active, 4000), PendingReviews: string(encoded),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// mergeReviewComments adds incoming's comments to the unit encoded in payload
// when both are the same review, skipping comments it already holds. It
// reports whether payload was that review's unit.
func mergeReviewComments(payload string, incoming workflow.ReviewUnit) (string, bool) {
	unit, err := workflow.DecodeReviewUnit(payload)
	if err != nil || unit.ReviewID != incoming.ReviewID {
		return payload, false
	}
	for _, c := range incoming.Comments {
		if !slices.ContainsFunc(unit.Comments, func(e workflow.ReviewUnitComment) bool { return e.CommentID == c.CommentID }) {
			unit.Comments = append(unit.Comments, c)
		}
	}
	encoded, err := workflow.EncodeReviewUnit(unit)
	if err != nil {
		return payload, false
	}
	return encoded, true
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
// transaction, under the same table check Requeue applies. retryMode is the
// operator's worktree choice for the next dispatch; the store persists it
// unread so the daemon's prepareWorkspace reads the mode the operator set.
// A resume copies the results recorded after its After stage onto the row, in
// the same transaction, so the attempt starts from what that stage left.
func (s *Store) RetryTask(ctx context.Context, taskID int64, fromStatus, wf, retryMode string, resume task.Resume) error {
	return s.requeue(ctx, taskID, fromStatus, wf, "retried "+wf, func(q *postgresdb.Queries) (int64, error) {
		results, err := resumeResults(ctx, q, taskID, resume)
		if err != nil {
			return 0, err
		}
		return q.RetryTask(ctx, postgresdb.RetryTaskParams{
			ID: taskID, Workflow: wf, FromStatus: fromStatus, RetryMode: retryMode,
			ResumeFrom: resume.From, ResumeResults: results,
		})
	})
}

// resumeResults reads the results a resumed attempt starts with: those of the
// After stage's most recent finished run, which must have completed.
func resumeResults(ctx context.Context, q *postgresdb.Queries, taskID int64, resume task.Resume) ([]byte, error) {
	if resume.From == "" || resume.After == "" {
		return []byte("{}"), nil
	}
	row, err := q.LatestStageResults(ctx, postgresdb.LatestStageResultsParams{ExecutionID: taskID, Name: resume.After})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Results == nil) {
		return nil, fmt.Errorf("%w: %q", storecontract.ErrResumeIncomplete, resume.After)
	}
	if err != nil {
		return nil, err
	}
	return row.Results, nil
}

// RespondReviewGate records the operator's gate answer and requeues, counting
// re-reviews against the cap under the row lock.
func (s *Store) RespondReviewGate(ctx context.Context, taskID int64, fromStatus, gate string, rereview bool, maxRounds int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := guardTransition(ctx, q, taskID, fromStatus, workflow.StatusQueued); err != nil {
		return err
	}
	// The row is locked by the guard read, so the cap decision and the
	// increment below cannot interleave with a second response write.
	rounds, err := q.RereviewRounds(ctx, taskID)
	if err != nil {
		return err
	}
	if rereview && rounds >= int64(maxRounds) {
		return storecontract.ErrRereviewCapReached
	}
	n, err := q.RespondReviewGateTask(ctx, postgresdb.RespondReviewGateTaskParams{
		ID: taskID, FromStatus: fromStatus, Gate: gate, Rereview: rereview, Cap: int64(maxRounds),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return storecontract.ErrStaleTransition
	}
	detail := "operator approved the review gate"
	if rereview {
		detail = "operator requested a re-review"
	}
	if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
		TaskID: taskID, FromStatus: fromStatus, ToStatus: workflow.StatusQueued, Detail: detail,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// RecoverStale requeues tasks left running and marks their running steps
// interrupted, in one transaction.
func (s *Store) RecoverStale(ctx context.Context) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	executions, err := q.LockRunningExecutions(ctx)
	if err != nil {
		return 0, err
	}
	for _, execution := range executions {
		steps, err := q.InterruptAttemptSteps(ctx, postgresdb.InterruptAttemptStepsParams{
			ExecutionID: execution.ID, Attempt: execution.Attempt,
		})
		if err != nil {
			return 0, err
		}
		for _, step := range steps {
			if _, err := insertEventQ(ctx, q, stepEvent(events.KindStageFinish, execution.ID,
				execution.Owner, execution.Repo, execution.IssueNumber, execution.Workflow,
				int(execution.Attempt), step.Name, map[string]any{"interrupted": true})); err != nil {
				return 0, err
			}
		}
		if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
			TaskID: execution.ID, FromStatus: workflow.StatusRunning, ToStatus: workflow.StatusQueued,
			Detail: "re-queued after a crashed or replaced daemon",
		}); err != nil {
			return 0, err
		}
	}
	n, err := q.RecoverStaleTasks(ctx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
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
		outputs, _ := task.DecodeOutputs(r.Outputs)
		tasks = append(tasks, workflow.Task{
			ID: r.ID, Owner: r.Owner, Repo: r.Repo, IssueNumber: int(r.IssueNumber),
			PRNumber: int(r.PrNumber), Status: r.Status, Source: r.Source,
			Identity: r.Identity, Attempt: int(r.Attempt),
			ReviewCursor: r.ReviewCursor, WatchCommentID: r.WatchCommentID,
			Outputs: outputs,
		})
	}
	return tasks, nil
}

// ClearTerminalTasks deletes tasks whose status is terminal.
func (s *Store) ClearTerminalTasks(ctx context.Context) (int64, error) {
	return s.queries().ClearTerminalTasks(ctx)
}

// Tasks returns the most recently updated tasks.
func (s *Store) Tasks(ctx context.Context, limit int) ([]workflow.Task, error) {
	return s.TasksPage(ctx, storecontract.TaskPage{Limit: limit})
}

// TasksPage returns one page of tasks, most recently updated first.
func (s *Store) TasksPage(ctx context.Context, page storecontract.TaskPage) ([]workflow.Task, error) {
	params := postgresdb.ListTaskSummariesParams{
		Statuses:  append([]string{}, page.Statuses...),
		PageLimit: int32(min(max(page.Limit, 1), 500)), //nolint:gosec // clamped
	}
	if page.After.ID != 0 {
		params.BeforeUpdated = pgtype.Timestamptz{Time: page.After.UpdatedAt, Valid: true}
		params.BeforeID = page.After.ID
	}
	rows, err := s.queries().ListTaskSummaries(ctx, params)
	if err != nil {
		return nil, err
	}
	tasks := make([]workflow.Task, 0, len(rows))
	for _, r := range rows {
		outputs, _ := task.DecodeOutputs(r.Outputs)
		tasks = append(tasks, workflow.Task{
			ID: r.ID, Owner: r.Owner, Repo: r.Repo, IssueNumber: int(r.IssueNumber),
			Title: r.Title, Status: r.Status, Workflow: r.Workflow,
			PRNumber: int(r.PrNumber), TokensUsed: int(r.TokensUsed), Iterations: int(r.Iterations),
			Attempt: int(r.Attempt), ParkReason: r.ParkReason, RetryCount: int(r.RetryCount),
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Plan: r.Plan, Source: r.Source,
			Identity: r.Identity, BindingID: r.BindingID, BindingVersion: int(r.BindingVersion),
			Outputs:    outputs,
			ReviewGate: r.ReviewGate, RereviewRounds: int(r.RereviewRounds),
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

// StartCall enqueues a workflow.call callee inheriting the caller's org,
// workspace, identity and repo. The caller must be running and within
// workflow.MaxCallDepth.
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

// callRefusal explains why EnqueueCallTask inserted nothing.
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

// CallStatus reads one call's callee for a waiting caller: its status, its
// latest transition detail and the outputs its finished run wrote. The parent
// check is the row check the wire grant cannot do: a caller reads only the
// tasks it started.
func (s *Store) CallStatus(ctx context.Context, callerTaskID, callTaskID int64) (string, string, map[string]any, error) {
	callee, err := s.queries().TaskByID(ctx, callTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, storecontract.ErrCallNotYours
	}
	if err != nil {
		return "", "", nil, err
	}
	if callee.CallParentTaskID != callerTaskID {
		return "", "", nil, storecontract.ErrCallNotYours
	}
	// The row is the callee's own; its outputs travel beside status and detail.
	// The caller
	// decides whether to use them -- only a successful terminal state's values
	// reach it.
	outputs, _ := task.DecodeOutputs(callee.Outputs)
	// A callee that has not moved yet has no transition row; its queue
	// status is the whole answer, and the empty detail says so.
	detail, err := s.queries().CallStatusDetail(ctx, callTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return callee.Status, "", outputs, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	return callee.Status, detail, outputs, nil
}

// ActiveTasksByOrigin returns the queued and running tasks the conversation
// origin created.
func (s *Store) ActiveTasksByOrigin(ctx context.Context, origin string) ([]*workflow.Task, error) {
	rows, err := s.queries().ActiveTasksByOrigin(ctx, origin)
	if err != nil {
		return nil, fmt.Errorf("store: active tasks by origin: %w", err)
	}
	out := make([]*workflow.Task, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskFromRow(row))
	}
	return out, nil
}
