-- Operator-selected retry worktree mode
-- (archie-core-vrl5: "retry should let the operator choose refresh-onto-base
-- or continue-pushed-work").
--
-- tasks.retry_mode records how the next dispatch positions the worktree:
-- 'refresh_onto_base' resets it onto the base branch, 'continue_pushed_work'
-- resumes the branch the task already pushed. The retry action writes the
-- operator's choice; BeginRemediationTask writes 'continue_pushed_work' when
-- it queues a remediation, because a remediation must continue its PR branch.
-- The default is the explicit refresh mode, so every existing row reads as a
-- retry that starts from base and no dispatch path infers the mode from the
-- workflow.

-- +goose Up

ALTER TABLE tasks ADD COLUMN retry_mode text NOT NULL DEFAULT 'refresh_onto_base';

-- +goose Down

ALTER TABLE tasks DROP COLUMN retry_mode;
