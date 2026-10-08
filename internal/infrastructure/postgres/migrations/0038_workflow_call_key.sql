-- tasks.call_key is a workflow.call call site's durable identity: the call
-- step's declared path within the calling run. With the parent it is the key
-- EnqueueCallTask upserts on, so a retry of one call site -- a lost
-- EnqueueCallTask reply, a failed step record, a requeued attempt -- returns
-- the child already started instead of starting a second one. The index is
-- partial: every task that is not a workflow.call callee carries the empty key.

-- +goose Up

ALTER TABLE tasks ADD COLUMN call_key text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_tasks_call_key ON tasks (call_parent_task_id, call_key) WHERE call_key <> '';

-- +goose Down

DROP INDEX idx_tasks_call_key;
ALTER TABLE tasks DROP COLUMN call_key;
