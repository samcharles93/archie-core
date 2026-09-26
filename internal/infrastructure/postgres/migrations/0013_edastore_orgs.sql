-- org_id and workspace_id on the event-capture records (the second phase of
-- the tenant boundary; the State Store tables carried them in 0012). Sources,
-- event types, mappings, bindings, captures and the tool_calls transcript are
-- workspace-owned; the dispatch ledgers are children of those records and
-- carry none. Like 0012, the default backfill is what an org-unaware writer
-- produces (docs/prds/orgs-and-access.md, step 1).

-- +goose Up

ALTER TABLE bindings ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE bindings ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE mappings ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE mappings ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE captures ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE captures ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE sources ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE sources ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE event_types ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE event_types ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';
ALTER TABLE tool_calls ADD COLUMN org_id text NOT NULL DEFAULT 'default';
ALTER TABLE tool_calls ADD COLUMN workspace_id text NOT NULL DEFAULT 'default';

-- +goose Down

ALTER TABLE tool_calls DROP COLUMN workspace_id;
ALTER TABLE tool_calls DROP COLUMN org_id;
ALTER TABLE event_types DROP COLUMN workspace_id;
ALTER TABLE event_types DROP COLUMN org_id;
ALTER TABLE sources DROP COLUMN workspace_id;
ALTER TABLE sources DROP COLUMN org_id;
ALTER TABLE captures DROP COLUMN workspace_id;
ALTER TABLE captures DROP COLUMN org_id;
ALTER TABLE mappings DROP COLUMN workspace_id;
ALTER TABLE mappings DROP COLUMN org_id;
ALTER TABLE bindings DROP COLUMN workspace_id;
ALTER TABLE bindings DROP COLUMN org_id;