-- +goose Up
-- The operator's accepted authority, recorded against the pinned digest. NULL
-- until the operator accepts; NULL is also the correct value for existing
-- rows, which were installed before acceptance records existed.
ALTER TABLE installed_packages ADD COLUMN accepted_authority jsonb;

-- +goose Down
ALTER TABLE installed_packages DROP COLUMN accepted_authority;