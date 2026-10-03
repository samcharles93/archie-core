-- Access policies and denial records (internal/domain/access)

-- name: ListAccessPolicies :many
SELECT level, org_id, workspace_id, object_kind, object_id, policy_id, text, updated_at
FROM access_policies ORDER BY level, org_id, workspace_id, object_kind, object_id, policy_id;

-- name: UpsertAccessPolicy :exec
INSERT INTO access_policies (level, org_id, workspace_id, object_kind, object_id, policy_id, text)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (level, org_id, workspace_id, object_kind, object_id, policy_id)
DO UPDATE SET text = excluded.text, updated_at = now();

-- name: GetAccessPolicy :one
SELECT text FROM access_policies
WHERE level = $1 AND org_id = $2 AND workspace_id = $3 AND object_kind = $4
  AND object_id = $5 AND policy_id = $6;

-- name: DeleteAccessPolicy :execrows
DELETE FROM access_policies
WHERE level = $1 AND org_id = $2 AND workspace_id = $3 AND object_kind = $4
  AND object_id = $5 AND policy_id = $6;

-- name: CountShippedOrgPolicies :one
SELECT count(*) FROM access_policies
WHERE level = 'org' AND org_id = $1 AND policy_id = ANY($2::text[]);

-- name: DeleteOrgPoliciesNotShipped :exec
DELETE FROM access_policies
WHERE level = 'org' AND org_id = $1 AND NOT (policy_id = ANY($2::text[]));

-- name: ResetInstancePolicies :exec
DELETE FROM access_policies WHERE level = 'instance';

-- name: UpsertAccessPolicyVersion :one
INSERT INTO access_policy_versions (level, org_id, workspace_id, version) VALUES ($1, $2, $3, 2)
ON CONFLICT (level, org_id, workspace_id)
DO UPDATE SET version = access_policy_versions.version + 1, updated_at = now()
RETURNING version;

-- name: DeleteAccessPolicyVersion :exec
DELETE FROM access_policy_versions v
WHERE v.level = $1 AND v.org_id = $2 AND v.workspace_id = $3
  AND NOT EXISTS (SELECT 1 FROM access_policies p
                  WHERE p.level = v.level AND p.org_id = v.org_id AND p.workspace_id = v.workspace_id);

-- name: InsertAccessPolicyAudit :exec
INSERT INTO sys_audit (at, table_name, record_key, field, old_value, new_value, record_version, actor, source, request_id)
VALUES (now(), 'access_policies', $1, 'text', $2, $3, $4, $5, $6, $7);

-- name: RecordDenial :exec
-- One row per (principal, action, resource, level, policies, minute);
INSERT INTO access_denials (org_id, principal, action, resource_kind, resource_id, level, policies)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (org_id, principal, action, resource_kind, resource_id, level, policies, minute_at)
DO UPDATE SET count = access_denials.count + 1, updated_at = now();

-- name: ListDenials :many
SELECT id, org_id, principal, action, resource_kind, resource_id, level, policies, minute_at, count, created_at, updated_at
FROM access_denials WHERE org_id = $1 ORDER BY id DESC LIMIT $2;

-- name: InsertAccessResetAudit :exec
INSERT INTO sys_audit (at, table_name, record_key, field, record_version, actor, source, request_id)
VALUES (now(), 'access_policies', $1, 'reset', 1, 'archied access reset', 'archied', '');
