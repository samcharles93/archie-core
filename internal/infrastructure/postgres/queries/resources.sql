-- name: LockResourceWrite :exec
-- This lock is acquired in its own statement before a resource read. PostgreSQL
-- takes a fresh Read Committed snapshot for the subsequent read, so concurrent
-- optimistic writers see the committed revision rather than both writing v1.
-- The lock is per (org, kind): two orgs writing the same kind do not contend.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(org_id)::text || '/' || sqlc.arg(kind)::text, 0));

-- name: ResourceByKind :one
SELECT kind, value, version, updated_at, org_id FROM resources
WHERE org_id = $1 AND kind = $2;

-- name: ListResources :many
SELECT kind, value, version, updated_at, org_id FROM resources
ORDER BY org_id, kind;

-- name: ResourceByRequest :one
SELECT org_id, kind, value, version, actor, source, request_id, expected_version,
       current_version, at
FROM resource_history
WHERE org_id = $1 AND kind = $2 AND request_id = $3;

-- name: ReinsertResource :one
-- Re-creates the current row for a revision resource_history already records.
-- The ledger outlives the row it describes, so a resource an operator removed
-- still has its revisions; putting one back needs the row re-created without a
-- second ledger entry, which idx_resource_history_org_kind_request would refuse.
-- The revision is the ledger's, not a fresh one: this is the row that write
-- produced, restored.
INSERT INTO resources (org_id, kind, value, version, updated_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING kind, value, version, updated_at, org_id;

-- name: ResourceHistory :many
SELECT org_id, kind, value, version, actor, source, request_id, expected_version,
       current_version, at
FROM resource_history
WHERE org_id = $1 AND kind = $2
ORDER BY version DESC
LIMIT NULLIF(sqlc.arg(entry_limit)::bigint, 0);

-- name: ResourceVersion :one
SELECT version FROM resources WHERE org_id = $1 AND kind = $2;

-- name: InsertResource :one
INSERT INTO resources (org_id, kind, value, version, updated_at)
VALUES ($1, $2, $3, 1, $4)
RETURNING kind, value, version, updated_at, org_id;

-- name: UpdateResource :one
UPDATE resources
SET value = $3, version = version + 1, updated_at = $4
WHERE org_id = $1 AND kind = $2 AND version = $5
RETURNING kind, value, version, updated_at, org_id;

-- name: InsertResourceHistory :one
INSERT INTO resource_history (
	org_id, kind, value, version, actor, source, request_id, expected_version,
	current_version, at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING org_id, kind, value, version, actor, source, request_id, expected_version,
	current_version, at;
