-- name: LockResourceWrite :exec
-- This lock is acquired in its own statement before a resource read. PostgreSQL
-- takes a fresh Read Committed snapshot for the subsequent read, so concurrent
-- optimistic writers see the committed revision rather than both writing v1.
SELECT pg_advisory_xact_lock(hashtextextended($1, 0));

-- name: ResourceByKind :one
SELECT kind, value, version, updated_at, org_id FROM resources WHERE kind = $1;

-- name: ResourceByRequest :one
SELECT kind, value, version, actor, source, request_id, expected_version,
       current_version, at
FROM resource_history
WHERE kind = $1 AND request_id = $2;

-- name: ResourceHistory :many
SELECT kind, value, version, actor, source, request_id, expected_version,
       current_version, at
FROM resource_history
WHERE kind = $1
ORDER BY version DESC
LIMIT NULLIF(sqlc.arg(entry_limit)::bigint, 0);

-- name: ResourceVersion :one
SELECT version FROM resources WHERE kind = $1;

-- name: InsertResource :one
INSERT INTO resources (kind, value, version, updated_at)
VALUES ($1, $2, 1, $3)
RETURNING kind, value, version, updated_at, org_id;

-- name: UpdateResource :one
UPDATE resources
SET value = $2, version = version + 1, updated_at = $3
WHERE kind = $1 AND version = $4
RETURNING kind, value, version, updated_at, org_id;

-- name: InsertResourceHistory :one
INSERT INTO resource_history (
	kind, value, version, actor, source, request_id, expected_version,
	current_version, at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING kind, value, version, actor, source, request_id, expected_version,
	current_version, at;
