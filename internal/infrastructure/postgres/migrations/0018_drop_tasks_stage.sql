-- tasks.stage was the one mutable column recording "which stage is this task
-- on", overwritten as workflow.Run advanced and carrying no history: a failed
-- attempt could not say which step failed or what ran before it without
-- replaying the event stream (docs/prds/execution-tree-state-machine.md,
-- "Problem"). step_executions (0017) is now the durable per-step record the
-- dashboard's run detail reads (ListSteps), so this column has no remaining
-- reader.

-- +goose Up
ALTER TABLE tasks DROP COLUMN stage;

-- +goose Down
ALTER TABLE tasks ADD COLUMN stage text NOT NULL DEFAULT '';
