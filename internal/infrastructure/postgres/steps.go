package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	task "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// The events land in the table; the caller publishes the returned event to its
// bus after the write commits, and the daemon's event sink skips
// already-persisted rows by their assigned ID -- the EmitDurable convention.

// StartStep creates a step and moves it to running, appending stage_start in
// the same transaction. Org and workspace come from the execution row.
func (s *Store) StartStep(ctx context.Context, start task.StepStart) (int64, events.Event, error) {
	if !task.ValidStepKind(start.Kind) {
		return 0, events.Event{}, fmt.Errorf("%w: unknown step kind %q", storecontract.ErrInvalidStep, start.Kind)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, events.Event{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	execution, err := q.LockExecutionForStep(ctx, start.ExecutionID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The execution the caller names does not exist: its view of the run
		// is stale, matching what guardTransition returns for a missing row.
		return 0, events.Event{}, storecontract.ErrStaleTransition
	}
	if err != nil {
		return 0, events.Event{}, err
	}
	if execution.Attempt != int64(start.Attempt) {
		// A step belongs to exactly one attempt; a caller naming one the
		// execution no longer runs is describing a run that is over.
		return 0, events.Event{}, storecontract.ErrStaleTransition
	}
	if err := s.verifyCallStep(ctx, q, start); err != nil {
		return 0, events.Event{}, err
	}

	depth, err := s.startParent(ctx, q, start)
	if err != nil {
		return 0, events.Event{}, err
	}
	// A step cannot reach running while its execution is not running, and
	// never under a terminal parent. Both are the state machine's rules, so
	// they refuse as illegal transitions.
	if err := taskstate.CheckStepStart(execution.Status, depth.underTerminal); err != nil {
		return 0, events.Event{}, fmt.Errorf("%w: %w", storecontract.ErrIllegalTransition, err)
	}

	stepID, err := q.InsertStepExecution(ctx, postgresdb.InsertStepExecutionParams{
		OrgID: execution.OrgID, WorkspaceID: execution.WorkspaceID,
		ExecutionID: execution.ID, Attempt: execution.Attempt,
		ParentID: stepParentID(start.ParentID), Depth: int32(depth.depth),
		Kind: start.Kind, Name: start.Name, CalledExecutionID: start.CalledExecutionID,
	})
	if err != nil {
		return 0, events.Event{}, err
	}
	n, err := q.StartStepExecution(ctx, stepID)
	if err != nil {
		return 0, events.Event{}, err
	}
	if n == 0 {
		return 0, events.Event{}, storecontract.ErrStaleTransition
	}

	event := stepEvent(events.KindStageStart, execution.ID,
		execution.Owner, execution.Repo, execution.IssueNumber, execution.Workflow,
		int(execution.Attempt), start.Name, nil)
	eventID, err := insertEventQ(ctx, q, event)
	if err != nil {
		return 0, events.Event{}, err
	}
	event.ID = eventID
	if err := tx.Commit(ctx); err != nil {
		return 0, events.Event{}, err
	}
	return stepID, event, nil
}

// verifyCallStep refuses a call step whose callee is not this execution's
// own.
func (s *Store) verifyCallStep(ctx context.Context, q *postgresdb.Queries, start task.StepStart) error {
	if start.CalledExecutionID == 0 {
		return nil
	}
	calleeParent, err := q.StepCalleeParent(ctx, start.CalledExecutionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.ErrStaleTransition
	}
	if err != nil {
		return err
	}
	if calleeParent != start.ExecutionID {
		return storecontract.ErrStaleTransition
	}
	return nil
}

// stepParent resolves the enclosing StepExecution a child step names: its
// row is locked in the same transaction, the tree must stay inside one
// execution and one attempt, and depth is derived from the parent, never
// supplied by the caller.
func (s *Store) startParent(ctx context.Context, q *postgresdb.Queries, start task.StepStart) (stepParentState, error) {
	if start.ParentID == 0 {
		return stepParentState{}, nil
	}
	parent, err := q.LockStepExecution(ctx, start.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return stepParentState{}, storecontract.ErrStaleTransition
	}
	if err != nil {
		return stepParentState{}, err
	}
	if parent.ExecutionID != start.ExecutionID || parent.Attempt != int64(start.Attempt) {
		// Trees never nest across executions, and a parent from another
		// attempt is a tree the caller is not running.
		return stepParentState{}, storecontract.ErrStaleTransition
	}
	return stepParentState{
		depth:         int(parent.Depth) + 1,
		underTerminal: taskstate.StepTerminal(taskstate.StepStatus(parent.Status)),
	}, nil
}

type stepParentState struct {
	depth         int
	underTerminal bool
}

// FinishStep moves a step to its outcome and returns the stage_finish event.
// A missing, foreign or mismatched step is stale; a disallowed pair is
// illegal.
func (s *Store) FinishStep(ctx context.Context, finish task.StepFinish) (events.Event, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return events.Event{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	step, err := q.LockStepExecution(ctx, finish.StepID)
	if errors.Is(err, pgx.ErrNoRows) {
		return events.Event{}, storecontract.ErrStaleTransition
	}
	if err != nil {
		return events.Event{}, err
	}
	// The step belongs to another run, or the caller believes a state the row
	// does not hold. Either way its view is stale, and writing would move
	// another execution's record.
	if step.ExecutionID != finish.ExecutionID || taskstate.StepStatus(step.Status) != finish.From {
		return events.Event{}, storecontract.ErrStaleTransition
	}
	// Staleness is decided first, the way guardTransition decides it: a caller
	// that is wrong about the row's state gets the stale sentinel even when
	// its pair is also unroutable.
	if !taskstate.CanStepTransition(finish.From, finish.To) {
		return events.Event{}, fmt.Errorf("%w: %s -> %s is not a step transition",
			storecontract.ErrIllegalTransition, finish.From, finish.To)
	}
	execution, err := q.TaskByID(ctx, step.ExecutionID)
	if err != nil {
		return events.Event{}, err
	}

	durationMS := int64(0)
	if step.StartedAt.Valid {
		durationMS = time.Since(step.StartedAt.Time).Milliseconds()
	}
	n, err := q.FinishStepExecution(ctx, postgresdb.FinishStepExecutionParams{
		Status: string(finish.To), Detail: clip(finish.Detail, 4000), TokensUsed: finish.TokensUsed,
		ID: finish.StepID, Status_2: string(finish.From),
		Results: finish.Results,
	})
	if err != nil {
		return events.Event{}, err
	}
	if n == 0 {
		return events.Event{}, storecontract.ErrStaleTransition
	}

	event := stepEvent(events.KindStageFinish, execution.ID,
		execution.Owner, execution.Repo, execution.IssueNumber, execution.Workflow,
		int(step.Attempt), step.Name,
		stepEventData(finish.To, durationMS, finish.Detail))
	eventID, err := insertEventQ(ctx, q, event)
	if err != nil {
		return events.Event{}, err
	}
	event.ID = eventID
	if err := tx.Commit(ctx); err != nil {
		return events.Event{}, err
	}
	return event, nil
}

// stepEvent builds the event row one step transition writes, stamped with the
// execution's identity the way TaskContext.Emit stamped it -- the same fields
// the timeline and the stage statistics read back.
func stepEvent(kind string, executionID int64, owner, repo string, issue int64, wf string, attempt int, stage string, data map[string]any) events.Event {
	return events.Event{
		Kind: kind, TaskID: executionID,
		Repo: owner + "/" + repo, Issue: int(issue), Workflow: wf,
		Attempt: attempt, Stage: stage, Data: data,
	}
}

// stepEventData reads a stage_finish event's data the way the dashboard
// (stageOutcome) and the stage statistics (StageStats) do: duration always,
// the error text only when the step reported failure, and the interruption
// marker only for an interrupted step.
func stepEventData(to taskstate.StepStatus, durationMS int64, detail string) map[string]any {
	data := map[string]any{"duration_ms": durationMS}
	switch to {
	case taskstate.StepFailed:
		data["error"] = detail
	case taskstate.StepInterrupted:
		data["interrupted"] = true
	}
	return data
}

// ListSteps returns an execution's steps, for every attempt when attempt is
// 0.
func (s *Store) ListSteps(ctx context.Context, executionID int64, attempt int) ([]task.StepExecution, error) {
	rows, err := s.queries().ListStepExecutions(ctx, postgresdb.ListStepExecutionsParams{
		ExecutionID: executionID, Attempt: int64(attempt),
	})
	if err != nil {
		return nil, err
	}
	steps := make([]task.StepExecution, len(rows))
	for i, r := range rows {
		steps[i] = task.StepExecution{
			ID: r.ID, ExecutionID: r.ExecutionID, Attempt: int(r.Attempt),
			ParentID: nullableInt64(r.ParentID), Depth: int(r.Depth),
			Kind: r.Kind, Name: r.Name, Status: taskstate.StepStatus(r.Status),
			Detail: r.Detail, TokensUsed: r.TokensUsed,
			StartedAt: r.StartedAt.Time, FinishedAt: r.FinishedAt.Time,
		}
	}
	return steps, nil
}

// nullableInt64 reads a pgtype.Int8 the way stepParentID wrote it: absent
// means 0, a stage at the tree's root.
func nullableInt64(v pgtype.Int8) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}

// stepParentID encodes the nullable parent id an insert takes: 0 means "no
// parent" -- a stage at the tree's root -- which the column stores as NULL
// rather than as a reference to a row that does not exist.
func stepParentID(parentID int64) pgtype.Int8 {
	return pgtype.Int8{Int64: parentID, Valid: parentID != 0}
}

// CancelExecution cancels the current attempt's open steps and moves the
// execution to `to`, in one transaction with events and an audit row.
func (s *Store) CancelExecution(ctx context.Context, taskID int64, reason, to string) ([]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	execution, err := q.LockExecutionForStep(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storecontract.ErrStaleTransition
	}
	if err != nil {
		return nil, err
	}
	if !taskstate.CanTransition(execution.Status, to) {
		return nil, fmt.Errorf("%w: %s -> %s is not a transition",
			storecontract.ErrIllegalTransition, execution.Status, to)
	}
	// Read the wait:true callees before the sweep below closes them: an open
	// call step is a callee the caller waits on, and one whose call step
	// already closed -- wait:false, or one that ended first -- runs on.
	waiting, err := q.WaitingCallSteps(ctx, postgresdb.WaitingCallStepsParams{
		ExecutionID: taskID, Attempt: execution.Attempt,
	})
	if err != nil {
		return nil, err
	}

	steps, err := q.CancelAttemptSteps(ctx, postgresdb.CancelAttemptStepsParams{
		ExecutionID: taskID, Attempt: execution.Attempt,
	})
	if err != nil {
		return nil, err
	}
	for _, step := range steps {
		if _, err := insertEventQ(ctx, q, stepEvent(events.KindStageFinish, execution.ID,
			execution.Owner, execution.Repo, execution.IssueNumber, execution.Workflow,
			int(execution.Attempt), step.Name, map[string]any{"cancelled": true})); err != nil {
			return nil, err
		}
	}

	n, err := q.TransitionTask(ctx, postgresdb.TransitionTaskParams{
		ID: taskID, Status: to, ParkReason: clip(reason, 4000),
		ParkClass: taskstate.ParkNeedsHuman, Status_2: execution.Status,
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, storecontract.ErrStaleTransition
	}
	if err := q.InsertTransition(ctx, postgresdb.InsertTransitionParams{
		TaskID: taskID, FromStatus: execution.Status, ToStatus: to, Detail: clip(reason, 4000),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID)
	}

	if err := s.cancelWaitedCallees(ctx, waiting, reason, to); err != nil {
		return ids, err
	}
	return ids, nil
}

// cancelWaitedCallees cancels each callee the caller was waiting on.
// Failures are reported; the caller's cancel stands.
func (s *Store) cancelWaitedCallees(ctx context.Context, waiting []postgresdb.WaitingCallStepsRow, reason, to string) error {
	var failures []error
	for _, call := range waiting {
		if _, err := s.CancelExecution(ctx, call.CalledExecutionID, reason, to); err != nil {
			failures = append(failures, fmt.Errorf("cancel callee %d: %w", call.CalledExecutionID, err))
		}
	}
	return errors.Join(failures...)
}
