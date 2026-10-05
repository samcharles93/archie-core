-- The resumable default org/workspace upgrade
-- (docs/prds/orgs-and-access.md, "Upgrading existing installs"). Each phase
-- runs in one transaction with its ledger row, so a phase is either recorded
-- or entirely absent; a recorded phase is skipped on the next start.

-- name: GetOrgUpgradePhase :one
SELECT completed_at FROM org_upgrades WHERE phase = $1;

-- name: InsertOrgUpgradePhase :exec
INSERT INTO org_upgrades (phase) VALUES ($1);

-- name: EnsureDefaultOrg :exec
INSERT INTO orgs (id, name) VALUES ('org-sys', 'System')
ON CONFLICT (id) DO NOTHING;

-- name: EnsureDefaultWorkspace :exec
INSERT INTO workspaces (id, org_id, name) VALUES ('default', 'org-sys', 'Default')
ON CONFLICT (org_id, id) DO NOTHING;

-- name: AssignDefaultOrgIdentities :exec
-- Every existing agent and service identity belongs to the default org.
INSERT INTO org_agents (identity_id, org_id)
SELECT id, 'org-sys' FROM identities WHERE kind IN ('bot', 'service_account')
ON CONFLICT (identity_id) DO NOTHING;

-- The phase stamps: empty values are rows written before the default
-- backfills took over, and re-stamping is harmless because the predicates
-- only ever match what the upgrade has not covered yet.

-- name: StampStateStoreTasks :execrows
UPDATE tasks SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampStateStoreEvents :execrows
UPDATE events SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampStateStoreTransitions :execrows
UPDATE transitions SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampStateStoreResources :execrows
UPDATE resources SET org_id = 'org-sys' WHERE org_id = '';

-- name: StampStateStoreResourceHistory :execrows
UPDATE resource_history SET org_id = 'org-sys' WHERE org_id = '';

-- name: StampEdastoreBindings :execrows
UPDATE bindings SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreMappings :execrows
UPDATE mappings SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreCaptures :execrows
UPDATE captures SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreSources :execrows
UPDATE sources SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreEventTypes :execrows
UPDATE event_types SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreToolCalls :execrows
UPDATE tool_calls SET org_id = 'org-sys', workspace_id = 'default' WHERE org_id = '';

-- The operator phase: if org-sys has no person member with the owner role,
-- create the dashboard's operator person and make it that owner. Both writes
-- re-check the condition, so a re-run on an install that already has a person
-- owner (or already has the operator) changes nothing.

-- name: EnsureOrgOperatorIdentity :execrows
INSERT INTO identities (id, kind, display_name, lifecycle, version, created_at, updated_at)
SELECT $1, 'user', 'Operator', 'active', 1, now(), now()
WHERE NOT EXISTS (
	SELECT 1 FROM memberships m
	JOIN identities i ON i.id = m.identity_id
	WHERE m.org_id = $2 AND m.workspace_id IS NULL AND m.role = 'owner' AND i.kind = 'user'
)
ON CONFLICT (id) DO NOTHING;

-- name: EnsureOrgOperatorMembership :execrows
INSERT INTO memberships (identity_id, org_id, workspace_id, role)
SELECT $1, $2, NULL, 'owner'
WHERE NOT EXISTS (
	SELECT 1 FROM memberships m
	JOIN identities i ON i.id = m.identity_id
	WHERE m.org_id = $2 AND m.workspace_id IS NULL AND m.role = 'owner' AND i.kind = 'user'
)
ON CONFLICT DO NOTHING;