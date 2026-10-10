-- A chat session belongs to one org; its messages and turns are reached only
-- through it. Sessions from before orgs belong to the system org.

-- +goose Up

ALTER TABLE sessions ADD COLUMN org_id text NOT NULL DEFAULT 'org-sys';
CREATE INDEX idx_sessions_org ON sessions (org_id);

-- +goose Down

DROP INDEX idx_sessions_org;
ALTER TABLE sessions DROP COLUMN org_id;
