-- +goose Up
CREATE TABLE harness_secrets (
    org TEXT NOT NULL,
    service TEXT NOT NULL,
    secret_enc TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org, service)
);

-- +goose Down
DROP TABLE harness_secrets;
