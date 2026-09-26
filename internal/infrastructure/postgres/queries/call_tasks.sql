-- name: EnqueueCallTask :one
-- The callee of a workflow.call step (docs/prds/workflow-calls.md). The
-- caller row is read FOR UPDATE, so two steps of the same run cannot race a
-- depth re-check, and the callee inherits the caller's org, workspace,
-- identity, owner and repo: a called workflow works the same run's context
-- under its own definition and profile. The issue number is a fresh
-- synthetic one for the inherited owner/repo, the same allocator
-- InsertChatTask uses. The insert refuses a caller that is not running and
-- a call that would pass the depth limit (the engine checks it too; the
-- store is what owns the table, so it re-checks).
WITH caller AS (
    SELECT t.* FROM tasks t WHERE t.id = $1 FOR UPDATE
)
INSERT INTO tasks (owner, repo, issue_number, title, body, labels, workflow, source, identity, org_id, workspace_id, inputs, call_parent_task_id, call_depth)
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
    caller.id, caller.call_depth + 1
FROM caller
WHERE caller.status = 'running' AND caller.call_depth + 1 <= sqlc.arg('max_depth')::int
RETURNING *;

-- name: CallStatusDetail :one
-- The latest transition detail of one call's callee, the summary a wait:true
-- caller receives (docs/prds/workflow-calls.md).
SELECT detail FROM transitions WHERE task_id = $1 ORDER BY id DESC LIMIT 1;