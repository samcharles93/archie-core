-- tasks.pending_reviews holds the review units that arrive while a
-- remediation already owns the task. Remediation runs for one task are
-- serialised (docs/prds/pr-review-remediation.md decision 5), so a distinct
-- review is appended here instead of dropped, and the oldest is promoted into
-- review_payload when the run that owned the task returns it to pr_open.
-- review_payload stays the active unit and is written by the run's own
-- Update; only this column carries the ones waiting behind it.

-- +goose Up

ALTER TABLE tasks ADD COLUMN pending_reviews jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down

ALTER TABLE tasks DROP COLUMN pending_reviews;
