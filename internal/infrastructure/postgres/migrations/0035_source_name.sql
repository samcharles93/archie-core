-- sources.name is the operator's label for a source; its path stays the
-- identity captures and bindings reference.

-- +goose Up

ALTER TABLE sources ADD COLUMN name text NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE sources DROP COLUMN name;
