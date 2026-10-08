-- name: EnqueueCallTask :one
-- The callee of a workflow.call step (docs/prds/workflow-calls.md)
--
-- The call key makes this the one write that can be retried: a call site that
-- already started its child gets that child back rather than a second one. The
-- upsert only fires for a keyed callee, and the WHERE below still decides
-- whether the caller may call at all -- a caller that is missing, not running
-- or past the depth limit inserts nothing and returns no row, conflict or not.
WITH caller AS (
    SELECT t.* FROM tasks t WHERE t.id = $1 FOR UPDATE
)
INSERT INTO tasks (owner, repo, issue_number, title, body, labels, workflow, source, identity, org_id, workspace_id, inputs, call_parent_task_id, call_depth, call_key)
SELECT
    caller.owner,
    caller.repo,
    COALESCE((
        SELECT MAX(existing.issue_number) FROM tasks existing
        WHERE existing.owner = caller.owner
          AND existing.repo = caller.repo
          AND existing.source = 'chat'
    ), sqlc.arg('fallback_issue_number')::bigint) + 1,
    sqlc.arg(title), sqlc.arg(body), 'chat', sqlc.arg(workflow), 'chat', caller.identity,
    caller.org_id, caller.workspace_id, sqlc.arg(inputs),
    caller.id, caller.call_depth + 1, sqlc.arg('call_key')
FROM caller
WHERE caller.status = 'running' AND caller.call_depth + 1 <= sqlc.arg('max_depth')::int
ON CONFLICT (call_parent_task_id, call_key) WHERE call_key <> '' DO UPDATE SET call_key = EXCLUDED.call_key
RETURNING *;

-- name: CallStatusDetail :one
-- The latest transition detail of one call's callee, the summary a wait:true
-- caller receives (docs/prds/workflow-calls.md).
SELECT detail FROM transitions WHERE task_id = $1 ORDER BY id DESC LIMIT 1;