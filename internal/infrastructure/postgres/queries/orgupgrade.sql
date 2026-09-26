-- The resumable default org/workspace upgrade
-- (docs/prds/orgs-and-access.md, "Upgrading existing installs"). Each phase
-- runs in one transaction with its ledger row, so a phase is either recorded
-- or entirely absent; a recorded phase is skipped on the next start.

-- name: GetOrgUpgradePhase :one
SELECT completed_at FROM org_upgrades WHERE phase = $1;

-- name: InsertOrgUpgradePhase :exec
INSERT INTO org_upgrades (phase) VALUES ($1);

-- name: EnsureDefaultOrg :exec
INSERT INTO orgs (id, name) VALUES ('default', 'Default')
ON CONFLICT (id) DO NOTHING;

-- name: EnsureDefaultWorkspace :exec
INSERT INTO workspaces (id, org_id, name) VALUES ('default', 'default', 'Default')
ON CONFLICT (org_id, id) DO NOTHING;

-- name: AssignDefaultOrgIdentities :exec
-- Every existing agent and service identity belongs to the default org.
INSERT INTO org_agents (identity_id, org_id)
SELECT id, 'default' FROM identities WHERE kind IN ('bot', 'service_account')
ON CONFLICT (identity_id) DO NOTHING;

-- The phase stamps: empty values are rows written before the default
-- backfills took over, and re-stamping is harmless because the predicates
-- only ever match what the upgrade has not covered yet.

-- name: StampStateStoreTasks :execrows
UPDATE tasks SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampStateStoreEvents :execrows
UPDATE events SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampStateStoreTransitions :execrows
UPDATE transitions SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampStateStoreResources :execrows
UPDATE resources SET org_id = 'default' WHERE org_id = '';

-- name: StampStateStoreResourceHistory :execrows
UPDATE resource_history SET org_id = 'default' WHERE org_id = '';

-- name: StampEdastoreBindings :execrows
UPDATE bindings SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreMappings :execrows
UPDATE mappings SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreCaptures :execrows
UPDATE captures SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreSources :execrows
UPDATE sources SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreEventTypes :execrows
UPDATE event_types SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';

-- name: StampEdastoreToolCalls :execrows
UPDATE tool_calls SET org_id = 'default', workspace_id = 'default' WHERE org_id = '';