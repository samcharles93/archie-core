-- The one-time SQLite import is removed, and with it the completion record
-- that gated serving over unimported legacy files.

-- +goose Up

DROP TABLE import_completion;

-- +goose Down

CREATE TABLE import_completion (
	id           bigint PRIMARY KEY CHECK (id = 1),
	completed_at timestamptz NOT NULL DEFAULT now(),
	sources      text NOT NULL,
	report       text NOT NULL
);
