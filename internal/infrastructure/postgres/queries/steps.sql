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
SET status = $1, detail = $2, tokens_used = $3, finished_at = now()
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
