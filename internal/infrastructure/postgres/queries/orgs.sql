-- Orgs, workspaces, memberships and agent assignments: the durable half of
-- the tenant boundary contract (internal/domain/org). IDs are caller-provided
-- slugs; workspaces are unique per org, memberships per identity+org+workspace
-- (schema 0012), and an agent assignment is unique per identity.

-- name: InsertOrg :exec
INSERT INTO orgs (id, name) VALUES ($1, $2);

-- name: GetOrg :one
SELECT id, name, created_at, updated_at FROM orgs WHERE id = $1;

-- name: ListOrgs :many
SELECT id, name, created_at, updated_at FROM orgs ORDER BY created_at, id;

-- name: InsertWorkspace :exec
INSERT INTO workspaces (id, org_id, name, environment) VALUES ($1, $2, $3, $4);

-- name: ListWorkspaces :many
SELECT id, org_id, name, environment, created_at, updated_at
FROM workspaces WHERE org_id = $1 ORDER BY created_at, id;

-- name: EnsureMembership :exec
INSERT INTO memberships (identity_id, org_id, workspace_id, role)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: ListMembershipsByIdentity :many
SELECT identity_id, org_id, workspace_id, role, created_at
FROM memberships WHERE identity_id = $1 ORDER BY created_at, org_id;

-- name: UpsertAgentAssignment :exec
INSERT INTO org_agents (identity_id, org_id) VALUES ($1, $2)
ON CONFLICT (identity_id) DO UPDATE SET org_id = excluded.org_id;

-- name: ResolveIdentityOrg :one
-- The assigned org wins for an agent; otherwise the identity's oldest
-- membership decides; an identity in no org belongs to the default org.
SELECT COALESCE(
	(SELECT a.org_id FROM org_agents a WHERE a.identity_id = $1),
	(SELECT m.org_id FROM memberships m WHERE m.identity_id = $1
	 ORDER BY m.created_at, m.org_id, m.workspace_id NULLS LAST LIMIT 1),
	'default'
)::text AS org_id;