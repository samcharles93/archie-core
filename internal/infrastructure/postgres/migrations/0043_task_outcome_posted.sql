-- tasks.outcome_posted marks that a chat task's terminal outcome reached the
-- conversation that created it. Tasks that finished before this column existed
-- were never promised a report.

-- +goose Up

ALTER TABLE tasks ADD COLUMN outcome_posted boolean NOT NULL DEFAULT false;
UPDATE tasks SET outcome_posted = true;
CREATE INDEX idx_tasks_outcome_unposted ON tasks (id) WHERE origin <> '' AND NOT outcome_posted;

-- +goose Down

DROP INDEX idx_tasks_outcome_unposted;
ALTER TABLE tasks DROP COLUMN outcome_posted;
