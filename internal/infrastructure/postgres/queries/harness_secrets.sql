-- Harness OAuth secret queries (docs/prds/external-agent-harness.md,
-- Credentials). One row per org/service; secret_enc holds the whole token
-- set (access token, refresh token, token type, expiry) as one encrypted
-- envelope over a JSON payload, never separate plaintext columns.

-- name: GetHarnessSecret :one
SELECT org, service, secret_enc, updated_at
FROM harness_secrets WHERE org = $1 AND service = $2;

-- name: PutHarnessSecret :exec
INSERT INTO harness_secrets (org, service, secret_enc, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (org, service) DO UPDATE SET secret_enc = EXCLUDED.secret_enc, updated_at = now();
