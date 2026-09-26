-- Workflow calls: the callee linkage on the task row
-- (docs/prds/workflow-calls.md).
--
-- tasks.call_parent_task_id names the task whose workflow.call step started
-- this one (0 on a root run) and call_depth counts workflow.call hops from
-- the root. Both default so every existing row and writer reads as a depth-0
-- root run; the depth limit itself is re-checked by the engine and the
-- enqueue, so no path can write past it.

-- +goose Up

ALTER TABLE tasks ADD COLUMN call_parent_task_id bigint NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN call_depth int NOT NULL DEFAULT 0;
CREATE INDEX idx_tasks_call_parent ON tasks (call_parent_task_id);

-- +goose Down

DROP INDEX idx_tasks_call_parent;
ALTER TABLE tasks DROP COLUMN call_depth;
ALTER TABLE tasks DROP COLUMN call_parent_task_id;