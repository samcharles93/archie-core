-- Event-log queries. The (at, id) cursor is a wire contract: at is the
-- fixed-width sort key and id only breaks ties, so a reader pages by
-- "after (at, id)" and never skips or repeats a row. See
-- internal/domain/storecontract/cursor.go.

-- name: InsertEvent :one
-- InsertEvent takes the event-append lock before the row draws its id, and
-- holds it until the enclosing transaction commits, so ids become visible in
-- the order they were drawn. The lock is inside the statement so a standalone
-- insert and one inside a longer transaction both take it.
WITH append_lock AS (
    SELECT pg_advisory_xact_lock(hashtextextended('archie.events.insert', 0))
)
INSERT INTO events (at, kind, task_id, repo, issue, workflow, stage, attempt, actor_id, actor_kind, principal_id, detail, data)
SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
FROM append_lock
RETURNING id;

-- name: ListEventsFromBeginning :many
SELECT id, at, kind, task_id, repo, issue, workflow, stage, attempt, actor_id, actor_kind, principal_id, detail, data
FROM events
ORDER BY at, id
LIMIT $1;

-- name: ListEventsAfter :many
SELECT id, at, kind, task_id, repo, issue, workflow, stage, attempt, actor_id, actor_kind, principal_id, detail, data
FROM events
WHERE at > $1 OR (at = $1 AND id > $2)
ORDER BY at, id
LIMIT $3;

-- name: TaskEventsByID :many
SELECT id, at, kind, task_id, repo, issue, workflow, stage, attempt, actor_id, actor_kind, principal_id, detail, data
FROM events
WHERE task_id = $1
ORDER BY id;

-- name: WorkflowStats :many
SELECT workflow,
       COUNT(*)::int AS runs,
       COUNT(*) FILTER (WHERE status = 'merged')::int AS merged,
       COUNT(*) FILTER (WHERE status = 'pr_open')::int AS pr_open,
       COUNT(*) FILTER (WHERE status = 'parked')::int AS parked,
       CAST(AVG(tokens_used) AS bigint) AS avg_tokens,
       AVG(iterations)::float8 AS avg_steps,
       SUM(tokens_used)::bigint AS total_tokens
FROM tasks
WHERE workflow <> ''
GROUP BY workflow
ORDER BY COUNT(*) DESC;

-- name: StageStats :many
SELECT workflow, stage,
       COUNT(*)::int AS runs,
       CAST(AVG((data::jsonb ->> 'duration_ms')::numeric) AS bigint) AS avg_ms,
       COUNT(*) FILTER (WHERE (data::jsonb ->> 'error') IS NOT NULL)::int AS errors
FROM events
WHERE kind = 'stage_finish'
GROUP BY workflow, stage
ORDER BY workflow, stage;

-- name: TokensByDay :many
WITH event_totals AS (
    SELECT task_id, to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
        SUM(CASE WHEN jsonb_typeof(data::jsonb -> 'tokens') = 'number'
            THEN (data::jsonb ->> 'tokens')::bigint ELSE 0 END) AS tokens,
        SUM(CASE WHEN jsonb_typeof(data::jsonb -> 'tokens') = 'number' THEN 1 ELSE 0 END) AS valid_tokens
    FROM events WHERE kind = 'agent_finish'
    GROUP BY task_id, day
), usage AS (
    SELECT day, tokens FROM event_totals WHERE valid_tokens > 0
    UNION ALL
    SELECT to_char(t.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), t.tokens_used
    FROM tasks t
    WHERE t.tokens_used != 0 AND NOT EXISTS (
        SELECT 1 FROM event_totals e
        WHERE e.task_id = t.id AND e.valid_tokens > 0
    )
)
SELECT day, SUM(tokens)::bigint AS tokens
FROM usage GROUP BY day ORDER BY day DESC LIMIT $1;
