-- import_completion records that the one-time import of an existing install's
-- SQLite stores committed and passed verification. It is written in the same
-- transaction as the imported rows, so a record exists exactly when the data
-- does. An empty database is not evidence that an import succeeded, only that
-- nothing has written to it yet, so serving over non-empty legacy files
-- requires this row.

-- +goose Up

CREATE TABLE import_completion (
	id           bigint PRIMARY KEY CHECK (id = 1),
	completed_at timestamptz NOT NULL DEFAULT now(),
	sources      text NOT NULL,
	report       text NOT NULL
);

-- +goose Down

DROP TABLE import_completion;
