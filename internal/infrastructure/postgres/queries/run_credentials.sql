-- name: InsertRunCredential :exec
INSERT INTO run_credentials (digest, task_id, expires_at) VALUES ($1, $2, $3);

-- name: DeleteRunCredential :exec
DELETE FROM run_credentials WHERE digest = $1;

-- name: RunCredentialTask :one
SELECT task_id FROM run_credentials WHERE digest = $1 AND expires_at > $2;

-- name: DeleteExpiredRunCredentials :exec
DELETE FROM run_credentials WHERE expires_at <= $1;
