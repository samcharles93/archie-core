-- +goose Up
ALTER TABLE presence ADD COLUMN checks jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE presence DROP COLUMN checks;
