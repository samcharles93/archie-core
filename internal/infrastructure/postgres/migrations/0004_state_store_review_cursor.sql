-- review_cursor is the poll backstop's high-water mark over forge review IDs,
-- kept separate from watch_comment_id (the review-comment cursor) because the
-- two forge ID sequences are independent. 0001 omitted the column, so a dev
-- database that already applied 0001 needs this follow-up rather than a
-- rewrite of the original migration: goose tracks version numbers, not
-- checksums.

-- +goose Up

ALTER TABLE tasks ADD COLUMN review_cursor bigint NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE tasks DROP COLUMN review_cursor;
