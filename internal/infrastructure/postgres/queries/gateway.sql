-- Gateway conversation queries: sessions, messages, turns, full-text search.
-- These back internal/gateway's PostgreSQL SessionStore.

-- ── Sessions ────────────────────────────────────────────────────────────────

-- name: SaveSession :exec
INSERT INTO sessions (session_id, platform, bot_user, channel_id, thread_id,
	title, parent_session_id, branch_name, created_at, last_active_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (session_id) DO UPDATE SET
	platform = EXCLUDED.platform,
	bot_user = EXCLUDED.bot_user,
	channel_id = EXCLUDED.channel_id,
	thread_id = EXCLUDED.thread_id,
	title = EXCLUDED.title,
	parent_session_id = EXCLUDED.parent_session_id,
	branch_name = EXCLUDED.branch_name,
	created_at = EXCLUDED.created_at,
	last_active_at = EXCLUDED.last_active_at;

-- name: SessionByID :one
SELECT session_id, platform, bot_user, channel_id, thread_id,
	title, parent_session_id, branch_name, created_at, last_active_at
FROM sessions WHERE session_id = $1;

-- name: SessionsByChannel :many
SELECT session_id, platform, bot_user, channel_id, thread_id,
	title, parent_session_id, branch_name, created_at, last_active_at
FROM sessions
WHERE platform = $1 AND channel_id = $2
ORDER BY GREATEST(last_active_at, created_at) DESC;

-- name: ListSessions :many
SELECT session_id, platform, bot_user, channel_id, thread_id,
	title, parent_session_id, branch_name, created_at, last_active_at
FROM sessions
ORDER BY GREATEST(last_active_at, created_at) DESC;

-- name: DeleteSessionMessages :exec
DELETE FROM messages WHERE session_id = $1;

-- name: DeleteSessionTurns :exec
DELETE FROM turns WHERE session_id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE session_id = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_active_at = $2 WHERE session_id = $1;

-- ── Messages ────────────────────────────────────────────────────────────────

-- name: LockSessionMessages :exec
-- Serialises message appends per session so the monotonic clamp's MAX(ts)
-- read cannot race a concurrent append of the same session. Transaction-scoped:
-- released at commit/rollback, which is exactly the lifetime of the append.
SELECT pg_advisory_xact_lock(hashtextextended('messages:' || $1::text, 0));

-- name: InsertMessageClamped :exec
-- The append path's strictly-increasing timestamp. The clamp is computed in
-- SQL, not in Go: the CASE assigns MAX(ts)+1 when the caller's timestamp is
-- not ahead of the session's newest, else the caller's timestamp. A Go-side
-- read-then-write would race two concurrent appends to the same session.
INSERT INTO messages (message_id, session_id, source_id, sender, sender_id, role, text, ts)
VALUES ($1, $2, $3, $4, $5, $6, $7, (
	SELECT CASE
		WHEN MAX(m.ts) IS NOT NULL AND MAX(m.ts) >= $8 THEN MAX(m.ts) + 1
		ELSE $8
	END
	FROM messages m WHERE m.session_id = $2
))
ON CONFLICT (session_id, message_id) DO NOTHING;

-- name: InsertMessageAt :exec
-- Rewriting history places records deliberately, so no clamp applies.
INSERT INTO messages (message_id, session_id, source_id, sender, sender_id, role, text, ts)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (session_id, message_id) DO NOTHING;

-- name: MessageID :one
SELECT message_id FROM messages WHERE session_id = $1 AND message_id = $2;

-- name: RecentMessages :many
SELECT m.message_id, m.source_id, m.sender, m.sender_id, m.role, m.text, m.ts,
	COALESCE(s.channel_id, '') AS channel_id, COALESCE(s.thread_id, '') AS thread_id
FROM (
	SELECT msg.message_id, msg.source_id, msg.sender, msg.sender_id, msg.role, msg.text, msg.ts, msg.session_id
	FROM messages msg WHERE msg.session_id = $1 ORDER BY msg.ts DESC LIMIT $2
) m
LEFT JOIN sessions s ON s.session_id = m.session_id
ORDER BY m.ts ASC;

-- name: DeleteRecentMessages :execrows
DELETE FROM messages WHERE id IN (
	SELECT m.id FROM messages m WHERE m.session_id = $1 ORDER BY m.ts DESC LIMIT $2
);

-- name: DeleteMessagesByID :exec
DELETE FROM messages WHERE session_id = $1 AND message_id = ANY(sqlc.arg(ids)::text[]);

-- name: CountMessages :one
SELECT COUNT(*) FROM messages WHERE session_id = $1;

-- name: SearchMessagesCount :one
SELECT COUNT(*) FROM messages
WHERE session_id = $1 AND search @@ plainto_tsquery('simple', $2);

-- name: SearchMessagesPage :many
SELECT m.message_id, m.source_id, m.sender, m.sender_id, m.role, m.text, m.ts,
	COALESCE(s.channel_id, '') AS channel_id, COALESCE(s.thread_id, '') AS thread_id
FROM messages m
LEFT JOIN sessions s ON s.session_id = m.session_id
WHERE m.session_id = $1 AND m.search @@ plainto_tsquery('simple', $2)
ORDER BY ts_rank(m.search, plainto_tsquery('simple', $2)) DESC, m.ts DESC, m.id DESC
LIMIT $3 OFFSET $4;

-- name: SourceMessageTs :one
SELECT ts FROM messages
WHERE session_id = $1 AND message_id IN ($2, $3)
ORDER BY CASE WHEN message_id = $3 THEN 0 ELSE 1 END
LIMIT 1;

-- name: NextReplyAfterTs :one
SELECT sender, text FROM messages
WHERE session_id = $1 AND ts > $2 ORDER BY ts ASC LIMIT 1;

-- ── Turns ───────────────────────────────────────────────────────────────────

-- name: InsertTurn :execrows
INSERT INTO turns (turn_id, session_id, source_id, status, attempt, owner_id,
	input_message_id, assistant_message_id, partial_text,
	response_text, tool_calls, error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (turn_id) DO NOTHING;

-- name: TurnByID :one
SELECT turn_id, session_id, source_id, status, attempt, owner_id,
	input_message_id, assistant_message_id, partial_text,
	response_text, tool_calls, error, created_at, updated_at
FROM turns WHERE turn_id = $1;

-- name: TurnAttemptOwner :one
SELECT attempt, owner_id FROM turns WHERE turn_id = $1;

-- name: FinalizeTurn :execrows
UPDATE turns SET status = $2, error = $3, updated_at = $4
WHERE turn_id = $1 AND attempt = $5 AND owner_id = $6;

-- name: RetryTurn :execrows
UPDATE turns SET status = $2, attempt = $3, owner_id = $4, error = $5, updated_at = $6
WHERE turn_id = $1 AND attempt = $7 AND owner_id = $8;

-- name: RecoverTurns :exec
UPDATE turns SET status = 'failed', error = 'recovered after process restart', updated_at = $1
WHERE status IN ('accepted', 'running', 'partial') AND owner_id <> $2;

-- name: SaveTurn :execrows
UPDATE turns SET
	session_id = $2, source_id = $3, status = $4, attempt = $5, owner_id = $6,
	input_message_id = $7, assistant_message_id = $8, partial_text = $9,
	response_text = $10, tool_calls = $11, error = $12, created_at = $13, updated_at = $14
WHERE turn_id = $1 AND attempt = $5 AND owner_id = $6;

-- name: RecentTurns :many
SELECT turn_id, session_id, source_id, status, attempt, owner_id,
	input_message_id, assistant_message_id, partial_text,
	response_text, tool_calls, error, created_at, updated_at
FROM turns WHERE session_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListRecoverableTurns :many
SELECT turn_id, session_id, source_id, status, attempt, owner_id,
	input_message_id, assistant_message_id, partial_text,
	response_text, tool_calls, error, created_at, updated_at
FROM turns WHERE status IN ('accepted', 'running', 'partial') ORDER BY updated_at ASC;
