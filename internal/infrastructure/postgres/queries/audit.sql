-- name: InsertResourceAudit :exec
-- Records the fields one resource write changed, against the version it
-- replaced. Runs in the write's own transaction.
INSERT INTO sys_audit (at, table_name, record_key, field, old_value, new_value, record_version, actor, source, request_id)
SELECT sqlc.arg(at)::timestamptz, 'resources', sqlc.arg(kind)::text, d.field, d.old_value, d.new_value,
       sqlc.arg(version)::bigint, sqlc.arg(actor)::text, sqlc.arg(source)::text, sqlc.arg(request_id)::text
FROM audit_json_diff(
	(SELECT convert_from(h.value, 'UTF8')::jsonb FROM resource_history h
	 WHERE h.kind = sqlc.arg(kind)::text AND h.version = sqlc.arg(previous_version)::bigint),
	convert_from(sqlc.arg(value)::bytea, 'UTF8')::jsonb
) d
ORDER BY d.field;

-- name: AuditForRecords :many
SELECT id, at, table_name, record_key, field, old_value, new_value, record_version, actor, source, request_id
FROM sys_audit
WHERE table_name = sqlc.arg(table_name)::text AND record_key = ANY(sqlc.arg(record_keys)::text[])
ORDER BY id DESC
LIMIT sqlc.arg(entry_limit)::bigint;
