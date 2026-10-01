-- +goose Up
-- The operator-approval gate's conversation with the operator
-- (docs/prds/pr-review-operator-response.md). review_gate is the JSON
-- document the gate writes before it waits -- the scored findings, the head
-- SHA, the pull request's identity and the workflow the wait resumes -- and
-- the answer the response path fills in. The text type mirrors review_payload,
-- its sibling column: the package that owns the type (internal/domain/
-- workflow/prreview) owns the encoding, so the store never parses it.
ALTER TABLE tasks ADD COLUMN review_gate text NOT NULL DEFAULT '';

-- rereview_rounds counts granted operator re-reviews, capped by the guarded
-- response write itself (prreview.MaxRereviewRounds). Deliberately separate
-- from retry_count and remediation_rounds: one shared counter made unrelated
-- retries draw another phase's budget down.
ALTER TABLE tasks ADD COLUMN rereview_rounds bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE tasks DROP COLUMN rereview_rounds;
ALTER TABLE tasks DROP COLUMN review_gate;