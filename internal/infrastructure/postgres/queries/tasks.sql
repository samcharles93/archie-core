-- name: ClaimNextTask :one
-- FOR UPDATE SKIP LOCKED replaces the SQLite single-writer assumption: two
-- claimers running at once take different rows instead of blocking, so the
-- daemon no longer depends on holding the only connection to the file.
-- The claim also starts the attempt's output set empty: outputs is
-- attempt-scoped, so nothing the previous attempt wrote survives
-- (docs/prds/workflow-call-outputs.md).
UPDATE tasks
SET status = 'running', attempt = attempt + 1, outputs = '', updated_at = now()
WHERE id = (
    SELECT id FROM tasks
    WHERE status = 'queued'
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: TaskByID :one
SELECT * FROM tasks WHERE id = sqlc.arg(id) AND (sqlc.narg(scope_org)::text IS NULL OR org_id = sqlc.narg(scope_org));

-- name: TaskByIssue :one
SELECT * FROM tasks WHERE owner = $1 AND repo = $2 AND issue_number = $3;

-- name: TaskByPR :one
-- OpenTaskByPR is the live-task lookup: it only resolves a PR the task is
-- currently tracking as open, so a merged or rejected task does not read as
-- the owner of a PR number it no longer holds.
SELECT * FROM tasks WHERE owner = $1 AND repo = $2 AND pr_number = $3 AND status = $4;

-- name: ListTaskSummaries :many
-- The dashboard's list. This projection is deliberately narrow: Plan gates
-- the "Decision required" panel and Source labels the row, and widening it
-- back would send rows without them. outputs rides it because the task row's
-- own structured result is part of the summary a caller or the dashboard
-- reads (docs/prds/workflow-call-outputs.md, "Storage and wire").
-- review_gate and rereview_rounds join it deliberately: the offer the gate
-- wrote is what the operator reads before answering it
-- (docs/prds/pr-review-operator-response.md, "The review the operator
-- answers"), and the round count is the cap's visible half.
SELECT id, owner, repo, issue_number, title, status, workflow,
       pr_number, tokens_used, iterations, attempt, park_reason, retry_count,
       created_at, updated_at, plan, source, identity, binding_id, binding_version,
       outputs, review_gate, rereview_rounds
FROM tasks
WHERE (sqlc.narg(scope_org)::text IS NULL OR org_id = sqlc.narg(scope_org))
  AND (cardinality(@statuses::text[]) = 0 OR status = ANY(@statuses::text[]))
  AND (sqlc.narg(before_updated)::timestamptz IS NULL
       OR (updated_at, id) < (sqlc.narg(before_updated)::timestamptz, @before_id::bigint))
ORDER BY updated_at DESC, id DESC
LIMIT @page_limit;

-- name: CountTasksByStatus :many
SELECT status, count(*)::int AS count FROM tasks WHERE (sqlc.narg(scope_org)::text IS NULL OR org_id = sqlc.narg(scope_org)) GROUP BY status;

-- name: InsertChatTask :one
-- The synthetic issue number keeps chat-sourced tasks off the forge's real
-- issue numbers; this allocator is the single source of truth for it.
-- fallback_issue_number is only the seed for a repo's first chat task -- the
-- passed value is not the issue number that lands.
-- inputs is the chat task's own workflow inputs; a binding dispatch passes
-- its inputs here too, so this insert is the single writer of the column and
-- StampTaskBinding adds only the provenance.
INSERT INTO tasks (owner, repo, issue_number, title, body, labels, workflow, source, identity, org_id, inputs, origin)
VALUES (
    sqlc.arg(owner), sqlc.arg(repo),
    COALESCE((
        SELECT MAX(existing.issue_number) FROM tasks existing
        WHERE existing.owner = sqlc.arg(owner)
          AND existing.repo = sqlc.arg(repo)
          AND existing.source = 'chat'
    ), sqlc.arg(fallback_issue_number)) + 1,
    sqlc.arg(title), sqlc.arg(body), 'chat', sqlc.arg(workflow), 'chat', sqlc.arg(identity),
    COALESCE(
        (SELECT a.org_id FROM org_agents a WHERE a.identity_id = sqlc.arg(identity)),
        (SELECT m.org_id FROM memberships m WHERE m.identity_id = sqlc.arg(identity)
         ORDER BY m.created_at, m.org_id, m.workspace_id NULLS LAST LIMIT 1),
        'org-sys'
    ),
    sqlc.arg(inputs),
    sqlc.arg(origin)
)
RETURNING *;

-- name: ActiveTasksByOrigin :many
-- The queued and running tasks one conversation created, for its /stop.
SELECT * FROM tasks
WHERE origin = sqlc.arg(origin) AND origin <> '' AND status IN ('queued', 'running') AND (sqlc.narg(scope_org)::text IS NULL OR org_id = sqlc.narg(scope_org))
ORDER BY id;

-- name: StampTaskBinding :exec
UPDATE tasks SET binding_id = $2, binding_version = $3 WHERE id = $1;

-- name: UpdateTask :exec
UPDATE tasks SET workflow = $2, branch = $3, plan = $4, notes = $5,
    pr_number = $6, tokens_used = $7, iterations = $8, park_reason = $9,
    watch_comment_id = $10, retry_count = $11, remediation_rounds = $12,
    review_payload = $13, workflow_definition_version = $14,
    workflow_definition_digest = $15, workflow_definition_yaml = $16,
    outputs = $17, review_gate = $18, rereview_rounds = $19,
    retry_mode = $20, updated_at = now()
WHERE id = $1;

-- name: RereviewRounds :one
-- Read under the lock LockTaskStatus takes in the same transaction, so the
-- cap decision and the increment it guards cannot interleave with a second
-- response write.
SELECT rereview_rounds FROM tasks WHERE id = $1;

-- name: LockTaskStatus :one
-- Locks the task's row and returns its current status, so the staleness and
-- transition-table checks that decide a guarded write hold against a
-- concurrent claim or transition until the update in the same transaction.
SELECT status FROM tasks WHERE id = $1 FOR UPDATE;

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
INSERT INTO transitions (task_id, from_status, to_status, detail, org_id, workspace_id)
SELECT t.id, sqlc.arg(from_status), sqlc.arg(to_status), sqlc.arg(detail), t.org_id, t.workspace_id
FROM tasks t WHERE t.id = sqlc.arg(task_id);

-- name: ParkTask :execrows
UPDATE tasks
SET status = 'parked', park_reason = $2, park_class = $3, updated_at = now()
WHERE id = $1 AND status = $4;

-- name: ClearTerminalTasks :execrows
DELETE FROM tasks
WHERE status IN ('merged', 'rejected', 'dead', 'closed_wont_do', 'completed');

-- name: RecoverStaleTasks :execrows
UPDATE tasks SET status = 'queued', updated_at = now() WHERE status = 'running';

-- name: EnqueueIssue :execrows
-- The org is derived from the identity at the one place a task row is
-- written (an agent's assignment, else a membership, else the default org);
-- uniqueness follows the record's identity: two orgs may work the same
-- issue under their own identities, one may not work it twice.
INSERT INTO tasks (owner, repo, issue_number, title, body, labels, identity, org_id, workspace_id)
VALUES (
	$1, $2, $3, $4, $5, $6, $7,
	COALESCE(
		(SELECT a.org_id FROM org_agents a WHERE a.identity_id = $7),
		(SELECT m.org_id FROM memberships m WHERE m.identity_id = $7
		 ORDER BY m.created_at, m.org_id, m.workspace_id NULLS LAST LIMIT 1),
		'org-sys'
	),
	'default'
)
ON CONFLICT (org_id, identity, owner, repo, issue_number) DO NOTHING;

-- name: ClaimByIssue :one
-- The claim starts the attempt's output set empty, as ClaimNextTask does:
-- outputs is attempt-scoped (docs/prds/workflow-call-outputs.md).
UPDATE tasks SET status = 'running', attempt = attempt + 1, outputs = '', updated_at = now()
WHERE owner = $1 AND repo = $2 AND issue_number = $3 AND status = 'queued'
RETURNING *;

-- name: BeginRemediationTask :execrows
-- A remediation must continue the branch its pull request lives on, so it
-- records that dispatch mode explicitly (taskstate.RetryContinuePushedWork)
-- rather than leaving prepareWorkspace to infer it from the workflow.
UPDATE tasks SET status = 'queued', workflow = 'remediate', park_reason = '', park_class = 'needs_human', review_payload = $2, retry_mode = 'continue_pushed_work', resume_from = '', resume_results = '{}', updated_at = now()
WHERE id = $1 AND status = 'pr_open';

-- name: AppendPendingReview :execrows
-- A distinct review that arrived while a remediation owned the task. The two
-- containment tests are the dedup: a re-delivered reaction, or a poll that
-- re-publishes a review already queued, appends nothing, and one already
-- active in review_payload is not queued to run a second time.
UPDATE tasks SET pending_reviews = CASE
        WHEN review_payload = $2::text THEN pending_reviews
        WHEN pending_reviews @> jsonb_build_array($2::text) THEN pending_reviews
        ELSE pending_reviews || jsonb_build_array($2::text)
    END,
    updated_at = now()
WHERE id = $1 AND status IN ('queued', 'running') AND workflow = 'remediate';

-- name: PromotePendingReview :execrows
-- The run that owned the task returned it to pr_open with reviews still
-- waiting: the oldest becomes the active payload and the task is queued
-- again, so it is remediated after the run that just finished.
UPDATE tasks SET status = 'queued', workflow = 'remediate', park_reason = '',
    park_class = 'needs_human',
    review_payload = pending_reviews->>0, pending_reviews = pending_reviews - 0,
    retry_mode = 'continue_pushed_work', resume_from = '', resume_results = '{}',
    updated_at = now()
WHERE id = $1 AND status = 'pr_open' AND workflow = 'remediate'
  AND jsonb_array_length(pending_reviews) > 0;

-- name: LockReviewUnits :one
-- The remediation's review units, locked so a comment merge cannot race the
-- claim that freezes the active unit or the promotion of a pending one.
SELECT status, workflow, review_payload, pending_reviews::text AS pending_reviews
FROM tasks WHERE id = $1 FOR UPDATE;

-- name: SetReviewUnits :exec
UPDATE tasks SET review_payload = $2, pending_reviews = CAST(CAST(sqlc.arg(pending_reviews) AS text) AS jsonb), updated_at = now()
WHERE id = $1;

-- name: SetReviewCursors :execrows
UPDATE tasks SET review_cursor = $2, watch_comment_id = $3, updated_at = now()
WHERE id = $1 AND status = 'pr_open';

-- name: RequeueTask :execrows
UPDATE tasks SET status = 'queued',
    workflow = CASE WHEN @workflow::text = '' THEN workflow ELSE @workflow::text END,
    park_reason = '', park_class = 'needs_human', updated_at = now()
WHERE id = @id AND status = @from_status;

-- name: RetryTask :execrows
UPDATE tasks SET status = 'queued', retry_count = retry_count + 1,
    workflow = CASE WHEN @workflow::text = '' THEN workflow ELSE @workflow::text END,
    retry_mode = @retry_mode::text,
    resume_from = @resume_from::text, resume_results = @resume_results::jsonb,
    park_reason = '', park_class = 'needs_human', updated_at = now()
WHERE id = @id AND status = @from_status;

-- name: RespondReviewGateTask :execrows
-- The review gate response write (docs/prds/pr-review-operator-response.md,
-- Decision 2): one guarded requeue beside RetryTask/BeginRemediation. The
-- from-status guard and the transition table live in the store's transaction
-- around this statement; the rereview cap is enforced HERE, in the row, so
-- two simultaneous re-reviews cannot spend a round the cap forbids. An
-- approve passes rereview=false and bumps nothing.
UPDATE tasks SET status = 'queued',
    review_gate = @gate::text,
    rereview_rounds = rereview_rounds + CASE WHEN @rereview::bool THEN 1 ELSE 0 END,
    park_reason = '', park_class = 'needs_human', updated_at = now()
WHERE id = @id AND status = @from_status
  AND (@rereview::bool = false OR rereview_rounds < @cap::bigint);

-- name: ArchiveTaskDelete :execrows
DELETE FROM tasks WHERE id = $1 AND status = $2;

-- name: ListOpenPRs :many
SELECT id, owner, repo, issue_number, pr_number, status, source, identity, attempt, review_cursor, watch_comment_id, outputs
FROM tasks WHERE status = 'pr_open';
