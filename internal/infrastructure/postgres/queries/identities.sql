-- Identity persistence. The domain owns lifecycle semantics (internal/domain/
-- identity); these queries are the durable half of that contract. Aliases are
-- unique on lower(alias) rather than the case-sensitive PRIMARY KEY, matching
-- the SQLite schema's COLLATE NOCASE.

-- name: ListIdentities :many
SELECT id, kind, display_name, lifecycle, version, created_at, updated_at
FROM identities ORDER BY created_at, id;

-- name: GetIdentity :one
SELECT id, kind, display_name, lifecycle, version, created_at, updated_at
FROM identities WHERE id = $1;

-- name: ResolveIdentityAlias :one
SELECT i.id, i.kind, i.display_name, i.lifecycle, i.version, i.created_at, i.updated_at
FROM identities i JOIN identity_aliases a ON a.identity_id = i.id
WHERE lower(a.alias) = lower($1);

-- name: InsertIdentity :exec
INSERT INTO identities (id, kind, display_name, lifecycle, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertIdentityAlias :exec
INSERT INTO identity_aliases (alias, identity_id) VALUES ($1, $2);

-- name: InsertIdentityAliasIgnoreConflict :exec
INSERT INTO identity_aliases (alias, identity_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: InsertIdentityEvent :exec
INSERT INTO identity_events (identity_id, event_type, from_lifecycle, to_lifecycle, display_name, actor_id, source, request_id, at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateIdentity :execrows
UPDATE identities SET display_name = $2, lifecycle = $3, version = $4, updated_at = $5
WHERE id = $1 AND version = $6;

-- name: ResolveIdentitySubject :one
SELECT i.id, i.kind, i.display_name, i.lifecycle, i.version, i.created_at, i.updated_at
FROM identities i JOIN identity_subjects b ON b.identity_id = i.id
WHERE b.issuer = $1 AND b.subject = $2;

-- name: UpsertIdentitySubject :exec
INSERT INTO identity_subjects (issuer, subject, identity_id, bound_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (issuer, subject) DO UPDATE SET identity_id = excluded.identity_id, bound_at = excluded.bound_at;

-- name: UpdateTaskIdentity :exec
UPDATE tasks SET identity = $1 WHERE identity = $2;
