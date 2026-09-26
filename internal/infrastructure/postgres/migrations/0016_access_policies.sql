-- Access policies and denial records: the stored half of the policy chain
-- (docs/prds/orgs-and-access.md, "Storing and changing policies" and
-- "Denials"). The shipped cross-org forbid is engine-enforced and has no row
-- here; the shipped role policies are seeded per org as ordinary rows with
-- the shipped IDs, so a reset overwrites them and an edit sticks until reset.
--
-- The scope tuple (level, org_id, workspace_id, object_kind, object_id) is
-- the policy's place in the chain; policy_id names the document inside it.
-- The version counter is per level-scope: PutPolicy bumps it and returns it,
-- so the engine's next load can detect a change.

-- +goose Up

CREATE TABLE access_policies (
	level        text NOT NULL CHECK (level IN ('instance', 'org', 'workspace', 'object')),
	org_id       text NOT NULL DEFAULT '',
	workspace_id text NOT NULL DEFAULT '',
	object_kind  text NOT NULL DEFAULT '',
	object_id    text NOT NULL DEFAULT '',
	policy_id    text NOT NULL,
	text         text NOT NULL,
	updated_at   timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (level, org_id, workspace_id, object_kind, object_id, policy_id)
);

CREATE TABLE access_policy_versions (
	level        text NOT NULL CHECK (level IN ('instance', 'org', 'workspace', 'object')),
	org_id       text NOT NULL DEFAULT '',
	workspace_id text NOT NULL DEFAULT '',
	version      bigint NOT NULL DEFAULT 1,
	updated_at   timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (level, org_id, workspace_id)
);

-- One denial row per (principal, action, resource, level, policies, minute):
-- a retry storm inside one minute is a count bump, not a row flood
-- (docs/prds/orgs-and-access.md, "Audit retention").
CREATE TABLE access_denials (
	id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	org_id        text NOT NULL,
	principal     text NOT NULL,
	action        text NOT NULL,
	resource_kind text NOT NULL,
	resource_id   text NOT NULL,
	level         text NOT NULL,
	policies      text[] NOT NULL DEFAULT '{}',
	minute_at     timestamptz NOT NULL DEFAULT date_trunc('minute', now()),
	count         bigint NOT NULL DEFAULT 1,
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_access_denials_dedup ON access_denials
	(org_id, principal, action, resource_kind, resource_id, level, policies, minute_at);

-- +goose Down

DROP INDEX idx_access_denials_dedup;
DROP TABLE access_denials;
DROP TABLE access_policy_versions;
DROP TABLE access_policies;