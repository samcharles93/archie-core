-- +goose Up
-- An update waiting for approval of widened authority, and the pin (with its
-- accepted authority) the last update or rollback replaced.
ALTER TABLE installed_packages
    ADD COLUMN pending_reference text,
    ADD COLUMN pending_digest text,
    ADD COLUMN pending_authority jsonb,
    ADD COLUMN previous_reference text,
    ADD COLUMN previous_digest text,
    ADD COLUMN previous_accepted_authority jsonb;

-- +goose Down
ALTER TABLE installed_packages
    DROP COLUMN pending_reference,
    DROP COLUMN pending_digest,
    DROP COLUMN pending_authority,
    DROP COLUMN previous_reference,
    DROP COLUMN previous_digest,
    DROP COLUMN previous_accepted_authority;
