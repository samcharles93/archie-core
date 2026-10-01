-- Declared workflow outputs on the task row
-- (docs/prds/workflow-call-outputs.md, "Storage and wire").
--
-- tasks.outputs is the JSON object of declared-output values a run wrote,
-- written by the run's own row write exactly as tasks.inputs is. The column
-- defaults to the empty string, so every existing row and writer reads as a
-- run that wrote nothing (the empty set). It is attempt-scoped: the claim
-- statements reset it alongside the attempt increment, so nothing a previous
-- attempt wrote survives into the next.

-- +goose Up

ALTER TABLE tasks ADD COLUMN outputs text NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE tasks DROP COLUMN outputs;
