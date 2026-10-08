-- tasks.call_key is a workflow.call call site's durable identity: the call
-- step's declared path within the calling run, plus the callee it starts. With
-- the parent it is the key EnqueueCallTask upserts on, so a retry of one call
-- site -- a lost EnqueueCallTask reply, a failed step record, a requeued
-- attempt -- returns the child already started instead of starting a second
-- one. The index is partial: every task that is not a workflow.call callee
-- carries the empty key.
--
-- A child that reached dead or closed_wont_do (taskstate.Dead and
-- taskstate.Declined) can never do the work and holds no result the caller can
-- use, so it leaves the index and frees its key for the caller's retry to
-- start a fresh child. parked and rejected stay in: a parked child is
-- recoverable by retrying the child -- which is what the caller's own park
-- names -- and a rejected child ran, so reusing it is what the call site wants.

-- +goose Up

ALTER TABLE tasks ADD COLUMN call_key text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_tasks_call_key ON tasks (call_parent_task_id, call_key)
    WHERE call_key <> '' AND status NOT IN ('dead', 'closed_wont_do');

-- +goose Down

DROP INDEX idx_tasks_call_key;
ALTER TABLE tasks DROP COLUMN call_key;
