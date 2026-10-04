-- A run credential authorizes one task's container: its State Store calls
-- and its branch push. Only the SHA-256 of the token is kept, so the table
-- cannot be replayed, and a row outlives a restart of either service.

-- +goose Up

CREATE TABLE run_credentials (
	digest     bytea PRIMARY KEY,
	task_id    bigint NOT NULL,
	expires_at timestamptz NOT NULL
);

-- +goose Down

DROP TABLE run_credentials;
