-- Orgs and workspaces: the tenant boundary (docs/prds/orgs-and-access.md,
-- step 1). The default org/workspace upgrade itself is a resumable data
-- upgrade recorded in org_upgrades and run by the State Store at boot
-- (internal/infrastructure/postgres/orgs.go), not part of the schema.
--
-- IDs are caller-provided slugs: they name the tenant in logs, keys and
-- cross-store references, and no slugs-as-uuids indirection is needed.
-- org_id/workspace_id columns here default to 'default': every writer that
-- is still org-unaware (the daemon, the binding editor) writes records of
-- the default org, which is exactly a single-operator install's semantics.
-- The default upgrade stamps any empty value left over and records the
-- phase ledger.

-- +goose Up

CREATE TABLE orgs (
	id         text PRIMARY KEY,
	name       text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspaces (
	id          text NOT NULL,
	org_id      text NOT NULL REFERENCES orgs(id),
	name        text NOT NULL,
	environment text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (org_id, id)
);

-- A membership gives an identity a role in an org, or in one workspace of it
-- when workspace_id is set. Unique on the coalesced workspace so an org-wide
-- membership cannot be duplicated: Postgres treats NULLs as distinct in a
-- unique index, so the PK cannot carry that rule.
CREATE TABLE memberships (
	identity_id text NOT NULL REFERENCES identities(id),
	org_id      text NOT NULL REFERENCES orgs(id),
	workspace_id text,
	role        text NOT NULL,
	created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_memberships_identity ON memberships
	(identity_id, org_id, COALESCE(workspace_id, ''));

-- An agent or service identity is assigned to exactly one org.
CREATE TABLE org_agents (
	identity_id text PRIMARY KEY REFERENCES identities(id),
	org_id      text NOT NULL REFERENCES orgs(id),
	created_at  timestamptz NOT NULL DEFAULT now()
);

-- org_upgrades records that a phase of the resumable default org/workspace
-- upgrade committed. Written in the same transaction as the phase's stamping,
-- so a phase is either recorded or entirely absent.
CREATE TABLE org_upgrades (
	phase        text PRIMARY KEY CHECK (phase IN ('state_store', 'edastore')),
	completed_at timestamptz NOT NULL DEFAULT now()
);

-- org_id and workspace_id on every owned record. The control-plane resource
-- documents are org-owned, not workspace-owned; run events and transitions
-- are workspace-owned children of runs (tasks).
ALTER TABLE tasks ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE tasks ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE events ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE events ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE transitions ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE transitions ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE resources ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE resource_history ADD COLUMN org_id text NOT NULL DEFAULT 'default';

-- Task uniqueness moves from (owner, repo, issue_number) to the record's
-- identity: two orgs may work the same issue under their own identities.
-- The migration backfill gives every existing row the same org, so the new
-- constraint holds wherever the old one did.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_owner_repo_issue_number_key;
ALTER TABLE tasks ADD CONSTRAINT tasks_org_identity_owner_repo_number_key
	UNIQUE (org_id, identity, owner, repo, issue_number);

-- +goose Down

DROP INDEX idx_memberships_identity;
DROP TABLE org_agents;
DROP TABLE memberships;
DROP TABLE workspaces;
DROP TABLE orgs;
DROP TABLE org_upgrades;

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_org_identity_owner_repo_number_key;
ALTER TABLE tasks ADD CONSTRAINT tasks_owner_repo_issue_number_key UNIQUE (owner, repo, issue_number);
ALTER TABLE resource_history DROP COLUMN org_id;
ALTER TABLE resources DROP COLUMN org_id;
ALTER TABLE transitions DROP COLUMN workspace_id;
ALTER TABLE transitions DROP COLUMN org_id;
ALTER TABLE events DROP COLUMN workspace_id;
ALTER TABLE events DROP COLUMN org_id;
ALTER TABLE tasks DROP COLUMN workspace_id;
ALTER TABLE tasks DROP COLUMN org_id;