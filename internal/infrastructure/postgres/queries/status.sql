-- Status projection queries: the published configuration snapshot and the
-- channel / apply-status runtime reports the UI reads across the State Store
-- boundary.

-- name: UpsertConfigSnapshot :exec
INSERT INTO config_snapshot (id, schema, document, published_at)
VALUES (1, $1, $2, $3)
ON CONFLICT (id) DO UPDATE SET
    schema = excluded.schema,
    document = excluded.document,
    published_at = excluded.published_at;

-- name: ConfigSnapshotByID :one
SELECT schema, document, published_at FROM config_snapshot WHERE id = 1;

-- name: UpsertChannelStatus :exec
INSERT INTO channel_status (id, name, state, detail, configured, reload_supported, observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    state = excluded.state,
    detail = excluded.detail,
    configured = excluded.configured,
    reload_supported = excluded.reload_supported,
    observed_at = excluded.observed_at;

-- name: DeleteChannelStatusNotIn :exec
DELETE FROM channel_status WHERE NOT (id = ANY($1::text[]));

-- name: ListChannelStatus :many
SELECT id, name, state, detail, configured, reload_supported, observed_at
FROM channel_status ORDER BY id;

-- name: UpsertApplyStatus :exec
INSERT INTO apply_status (process, kind, applied_version, error, reported_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (process, kind) DO UPDATE SET
    applied_version = excluded.applied_version,
    error = excluded.error,
    reported_at = excluded.reported_at;

-- name: ListApplyStatus :many
SELECT process, kind, applied_version, error, reported_at
FROM apply_status ORDER BY process, kind;

-- name: UpsertPresence :exec
INSERT INTO presence (service, instance_id, version, install_type, started_at, reported_at, ready, detail, checks)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (service, instance_id) DO UPDATE SET
    version = excluded.version,
    install_type = excluded.install_type,
    started_at = excluded.started_at,
    reported_at = excluded.reported_at,
    ready = excluded.ready,
    detail = excluded.detail,
    checks = excluded.checks;

-- name: DeletePresenceBefore :exec
DELETE FROM presence WHERE reported_at < $1;

-- name: ListPresence :many
SELECT service, instance_id, version, install_type, started_at, reported_at, ready, detail, checks
FROM presence ORDER BY service, instance_id;
