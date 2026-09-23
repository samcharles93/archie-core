-- name: ClaimNextTask :one
-- FOR UPDATE SKIP LOCKED replaces the SQLite single-writer assumption: two
-- claimers running at once take different rows instead of blocking, so the
-- daemon no longer depends on holding the only connection to the file.
UPDATE tasks
SET status = 'running', attempt = attempt + 1, updated_at = now()
WHERE id = (
    SELECT id FROM tasks
    WHERE status = 'queued'
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: TaskByID :one
SELECT * FROM tasks WHERE id = $1;

-- name: TaskByIssue :one
SELECT * FROM tasks WHERE owner = $1 AND repo = $2 AND issue_number = $3;

-- name: TaskByPR :one
SELECT * FROM tasks WHERE owner = $1 AND repo = $2 AND pr_number = $3;

-- name: ListTaskSummaries :many
-- The dashboard's list. This projection is deliberately narrow: Plan gates
-- the "Decision required" panel and Source labels the row, and widening it
-- back would send rows without them.
SELECT id, owner, repo, issue_number, title, status, workflow, stage,
       pr_number, tokens_used, iterations, attempt, park_reason, retry_count,
       created_at, updated_at, plan, source, identity, binding_id, binding_version
FROM tasks ORDER BY updated_at DESC LIMIT $1;

-- name: CountTasksByStatus :many
SELECT status, count(*)::int AS count FROM tasks GROUP BY status;

-- name: InsertChatTask :one
-- The synthetic issue number keeps chat-sourced tasks off the forge's real
-- issue numbers; this allocator is the single source of truth for it.
-- fallback_issue_number is only the seed for a repo's first chat task -- the
-- passed value is not the issue number that lands.
INSERT INTO tasks (owner, repo, issue_number, title, body, labels, workflow, source, identity)
VALUES (
    sqlc.arg(owner), sqlc.arg(repo),
    COALESCE((
        SELECT MAX(existing.issue_number) FROM tasks existing
        WHERE existing.owner = sqlc.arg(owner)
          AND existing.repo = sqlc.arg(repo)
          AND existing.source = 'chat'
    ), sqlc.arg(fallback_issue_number)) + 1,
    sqlc.arg(title), sqlc.arg(body), 'chat', sqlc.arg(workflow), 'chat', sqlc.arg(identity)
)
RETURNING *;

-- name: StampTaskBinding :exec
UPDATE tasks SET binding_id = $2, binding_version = $3 WHERE id = $1;

-- name: UpdateTask :exec
UPDATE tasks SET workflow = $2, stage = $3, branch = $4, plan = $5, notes = $6,
    pr_number = $7, tokens_used = $8, iterations = $9, park_reason = $10,
    watch_comment_id = $11, retry_count = $12, remediation_rounds = $13,
    review_payload = $14, workflow_definition_version = $15,
    workflow_definition_digest = $16, workflow_definition_yaml = $17,
    updated_at = now()
WHERE id = $1;

-- name: TransitionTask :execrows
-- Arriving at 'parked' also records why. The class is normalized by the
-- caller, so an unknown value persists as needs_human rather than as itself.
UPDATE tasks
SET status = $2,
    park_reason = CASE WHEN $2 = 'parked' THEN $3 ELSE park_reason END,
    park_class = CASE WHEN $2 = 'parked' THEN $4 ELSE park_class END,
    updated_at = now()
WHERE id = $1 AND status = $5;

-- name: InsertTransition :exec
INSERT INTO transitions (task_id, from_status, to_status, detail)
VALUES ($1, $2, $3, $4);

-- name: ParkTask :execrows
UPDATE tasks
SET status = 'parked', park_reason = $2, park_class = $3, updated_at = now()
WHERE id = $1 AND status = $4;

-- name: ClearTerminalTasks :execrows
DELETE FROM tasks
WHERE status IN ('merged', 'rejected', 'dead', 'closed_wont_do');

-- name: RecoverStaleTasks :execrows
UPDATE tasks SET status = 'queued', updated_at = now() WHERE status = 'running';
