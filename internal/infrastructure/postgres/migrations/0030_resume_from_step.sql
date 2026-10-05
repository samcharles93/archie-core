-- step_executions.results is the run's step results (summary and structured
-- result, by step id) after a stage the run moved past. A resumed attempt
-- starts from the results of the stage before its resume point, so steps it
-- skips still answer {{ steps.<id>.* }}. NULL means the run did not move past
-- the stage.
--
-- tasks.resume_from names the stage the next attempt starts at, and
-- tasks.resume_results the results it starts with. A retry writes both; an
-- empty resume_from runs the workflow from its first stage.

-- +goose Up

ALTER TABLE step_executions ADD COLUMN results jsonb;
ALTER TABLE tasks ADD COLUMN resume_from text NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN resume_results jsonb NOT NULL DEFAULT '{}';

-- +goose Down

ALTER TABLE tasks DROP COLUMN resume_results;
ALTER TABLE tasks DROP COLUMN resume_from;
ALTER TABLE step_executions DROP COLUMN results;
