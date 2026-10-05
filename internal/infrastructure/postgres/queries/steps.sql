-- Step-execution queries (docs/prds/execution-tree-state-machine.md). The
-- store method owns the transaction that pairs each step transition with its
-- event row; these are only the guarded row writes and reads.

-- name: LockExecutionForStep :one
-- FOR UPDATE locks the execution's row, so the status check, the step insert
-- and the event append are one serialized operation per execution -- the way
-- guardTransition locks a task's row for its transition. The row also carries
-- every field the transition's event needs (org, workspace, the task
-- identity) and the attempt the step must belong to.
SELECT id, status, org_id, workspace_id, owner, repo, issue_number, workflow, attempt
FROM tasks WHERE id = $1 FOR UPDATE;

-- name: InsertStepExecution :one
INSERT INTO step_executions (org_id, workspace_id, execution_id, attempt, parent_id, depth, kind, name, called_execution_id, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending')
RETURNING id;

-- name: StepCalleeParent :one
-- The caller linkage a call step's callee must carry: the callee's
-- call_parent_task_id names the execution whose tree records the call. A
-- call step naming a task that is not this execution's callee is refused,
-- because a cancel of that execution would otherwise cancel work the
-- caller never started.
SELECT call_parent_task_id FROM tasks WHERE id = $1;

-- name: StartStepExecution :execrows
-- The pending -> running half of StartStep, guarded by the row's own status:
-- the step transition table routes the pair, so a row that is not pending is
-- stale rather than silently restarted.
UPDATE step_executions SET status = 'running', started_at = now()
WHERE id = $1 AND status = 'pending';

-- name: LockStepExecution :one
-- FOR UPDATE, shared by a start's parent check and a finish's guarded write:
-- the row is held for the caller's transaction, so the state a check reads is
-- the state the guarded write sees.
SELECT id, org_id, workspace_id, execution_id, attempt, parent_id, depth, kind, name, called_execution_id, status, detail, tokens_used, started_at, finished_at
FROM step_executions WHERE id = $1 FOR UPDATE;

-- name: FinishStepExecution :execrows
UPDATE step_executions
SET status = $1, detail = $2, tokens_used = $3, results = $6, finished_at = now()
WHERE id = $4 AND status = $5;
-- name: CancelAttemptSteps :many
-- The half of CancelExecution that cancels the current attempt's
-- non-terminal steps, returning them so the store writes one event row per
-- transition in the same transaction. Earlier attempts' rows never match:
-- history is never rewritten.
UPDATE step_executions
SET status = 'cancelled', finished_at = now()
WHERE execution_id = $1 AND attempt = $2 AND status IN ('pending', 'running')
RETURNING id, name;

-- name: LockRunningExecutions :many
-- The executions a crashed or replaced daemon left running, locked for the
-- recovery transaction so the step sweep and the requeue cannot race a
-- concurrent write. The row carries the identity the steps' events need.
SELECT id, org_id, workspace_id, owner, repo, issue_number, workflow, attempt
FROM tasks WHERE status = 'running' FOR UPDATE;

-- name: InterruptAttemptSteps :many
-- The crash-recovery step edge (docs/prds/execution-tree-state-machine.md):
-- the steps an interrupted execution left running move to the table's
-- interrupted outcome, guarded by the row's own status so only the pair the
-- step transition table routes is written. Returning them lets the store
-- write one event row per transition in the same transaction.
UPDATE step_executions
SET status = 'interrupted', finished_at = now()
WHERE execution_id = $1 AND attempt = $2 AND status = 'running'
RETURNING id, name;

-- name: ListStepExecutions :many
-- attempt = 0 lists every attempt of the execution, oldest first; the index
-- (execution_id, attempt, id) makes both this and the single-attempt form a
-- straight index scan.
SELECT id, execution_id, attempt, parent_id, depth, kind, name, status, detail, tokens_used, started_at, finished_at
FROM step_executions
WHERE execution_id = @execution_id::bigint AND (@attempt::bigint = 0 OR attempt = @attempt::bigint)
ORDER BY attempt, id;
-- name: WaitingCallSteps :many
-- The call steps of the execution's current attempt that are still open and
-- name a callee: a call step closes when the callee ends, so an open one is
-- a callee the caller waits on (wait:true). The callee sweep of CancelExecution
-- walks these, because a callee whose call step already closed -- wait:false,
-- or one that ended before the cancel arrived -- runs on.
SELECT id, called_execution_id FROM step_executions
WHERE execution_id = $1 AND attempt = $2
  AND kind = 'call' AND status IN ('pending', 'running') AND called_execution_id <> 0;

-- name: LatestStageResults :one
-- The most recent finished run of a root stage, across attempts: a resumed
-- attempt skips the stages before its resume point, so the last attempt that
-- ran this stage may be an earlier one. NULL results mean that run did not
-- move past the stage.
SELECT status, results FROM step_executions
WHERE execution_id = $1 AND depth = 0 AND kind = 'stage' AND name = $2
  AND status IN ('succeeded', 'failed')
ORDER BY attempt DESC, id DESC
LIMIT 1;
