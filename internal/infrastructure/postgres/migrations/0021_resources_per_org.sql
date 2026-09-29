-- +goose Up

-- A control-plane resource is one document per (org, kind). Every existing row
-- already carries org_id 'default' from 0013, so the wider key holds wherever
-- the kind-only one did.
ALTER TABLE resources DROP CONSTRAINT resources_pkey;
ALTER TABLE resources ADD PRIMARY KEY (org_id, kind);

DROP INDEX idx_resource_history_kind_version;
DROP INDEX idx_resource_history_kind_request;
CREATE INDEX idx_resource_history_org_kind_version ON resource_history (org_id, kind, version);
-- The request-ID idempotency key is per org and kind: a resource write replays
-- only the same org's prior write of the same kind (resourceByRequest).
CREATE UNIQUE INDEX idx_resource_history_org_kind_request ON resource_history (org_id, kind, request_id);

-- +goose Down

DROP INDEX idx_resource_history_org_kind_request;
DROP INDEX idx_resource_history_org_kind_version;
CREATE UNIQUE INDEX idx_resource_history_kind_request ON resource_history (kind, request_id);
CREATE INDEX idx_resource_history_kind_version ON resource_history (kind, version);

ALTER TABLE resources DROP CONSTRAINT resources_pkey;
ALTER TABLE resources ADD PRIMARY KEY (kind);
