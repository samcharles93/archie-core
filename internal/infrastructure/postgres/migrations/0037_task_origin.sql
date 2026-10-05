-- tasks.origin names the conversation that created a chat task, so a chat's
-- /stop can find the work its agent started. Empty for every other task.

-- +goose Up

ALTER TABLE tasks ADD COLUMN origin text NOT NULL DEFAULT '';
CREATE INDEX idx_tasks_origin ON tasks (origin) WHERE origin <> '';

-- +goose Down

DROP INDEX idx_tasks_origin;
ALTER TABLE tasks DROP COLUMN origin;
