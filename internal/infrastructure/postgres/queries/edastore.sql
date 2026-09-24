-- EDA queries: captures, mappings, bindings, dispatch ledgers, tool_calls.

-- name: InsertCapture :exec
INSERT INTO captures (id, source, remote_addr, content_type, headers, body, authenticated, received_at, unsigned, event_type)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListCaptures :many
SELECT id, source, remote_addr, content_type, headers, body, authenticated, received_at, unsigned, event_type
FROM captures
ORDER BY received_at DESC
LIMIT $1;

-- name: ListUndispatchedCaptures :many
-- A capture is undispatched while some armed binding on its source, whose
-- mapping belongs to the capture's event type, has not dispatched it. An
-- unidentified capture has no event type, so it is never listed.
SELECT c.id, c.source, c.remote_addr, c.content_type, c.headers, c.body, c.authenticated, c.received_at, c.unsigned, c.event_type
FROM captures c
WHERE c.source = ANY(@sources::text[])
  AND c.event_type <> ''
  AND EXISTS (
	SELECT 1 FROM bindings b JOIN mappings m ON m.id = b.mapping
	WHERE b.source = c.source AND b.status = 'armed' AND m.event_type = c.event_type
	  AND NOT EXISTS (SELECT 1 FROM binding_dispatches d WHERE d.binding = b.id AND d.capture = c.id)
  )
ORDER BY c.received_at DESC
LIMIT @entry_limit;

-- name: DeleteCapturesOlderThan :exec
DELETE FROM captures WHERE received_at < $1;

-- name: DeleteCapturesBeyondCount :exec
DELETE FROM captures WHERE id NOT IN (
	SELECT id FROM captures ORDER BY received_at DESC LIMIT $1
);

-- name: InsertMapping :exec
INSERT INTO mappings (id, name, source_hint, event_type, fields)
VALUES ($1, $2, $3, $4, $5);

-- name: GetMapping :one
SELECT sqlc.embed(mappings),
	(SELECT count(*) FROM mapping_matches mm WHERE mm.mapping = mappings.id)::bigint AS match_count,
	COALESCE((SELECT max(mm.matched_at) FROM mapping_matches mm WHERE mm.mapping = mappings.id), 'epoch')::timestamptz AS last_matched_at
FROM mappings WHERE id = $1;

-- name: ListMappings :many
SELECT sqlc.embed(mappings),
	(SELECT count(*) FROM mapping_matches mm WHERE mm.mapping = mappings.id)::bigint AS match_count,
	COALESCE((SELECT max(mm.matched_at) FROM mapping_matches mm WHERE mm.mapping = mappings.id), 'epoch')::timestamptz AS last_matched_at
FROM mappings ORDER BY created_at DESC;

-- name: UpdateMapping :execrows
UPDATE mappings
SET name = $2, source_hint = $3, event_type = $4, fields = $5, updated_at = now()
WHERE id = $1;

-- name: InsertMappingMatch :exec
INSERT INTO mapping_matches (mapping, capture) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DeleteMapping :execrows
DELETE FROM mappings WHERE id = $1;

-- name: InsertBinding :exec
INSERT INTO bindings (id, name, source, mapping, filter, workflow, owner, repo, version, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9);

-- name: GetBinding :one
SELECT id, name, source, mapping, workflow, owner, repo, version, status, created_at, updated_at, filter
FROM bindings WHERE id = $1;

-- name: ListBindings :many
SELECT id, name, source, mapping, workflow, owner, repo, version, status, created_at, updated_at, filter
FROM bindings ORDER BY created_at DESC;

-- name: ArmedBindingsForSource :many
SELECT id, name, source, mapping, workflow, owner, repo, version, status, created_at, updated_at, filter
FROM bindings WHERE source = $1 AND status = 'armed' ORDER BY created_at DESC;

-- name: UpdateBinding :execrows
UPDATE bindings
SET name = $2, source = $3, mapping = $4, filter = $5, workflow = $6, owner = $7, repo = $8,
    version = version + 1, status = $9, updated_at = now()
WHERE id = $1;

-- name: SetBindingArmed :execrows
UPDATE bindings SET status = 'armed', updated_at = now() WHERE id = $1;

-- name: DeleteBinding :execrows
DELETE FROM bindings WHERE id = $1;

-- name: InsertBindingDispatch :exec
INSERT INTO binding_dispatches (binding, binding_version, capture, task_id)
VALUES ($1, $2, $3, $4);

-- name: InsertPlaybookDispatch :exec
INSERT INTO playbook_dispatches (playbook_id, playbook_version, event_id, action_id)
VALUES ($1, $2, $3, $4);

-- name: DeletePlaybookDispatches :exec
DELETE FROM playbook_dispatches WHERE playbook_id = $1;

-- name: InsertToolCall :exec
INSERT INTO tool_calls (id, task_id, attempt, tool, result, error, called_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: TaskToolCalls :many
SELECT id, task_id, attempt, tool, result, error, called_at
FROM tool_calls WHERE task_id = $1 ORDER BY called_at ASC;

-- name: InsertEventType :exec
INSERT INTO event_types (id, source, name, rule, schema)
VALUES ($1, $2, $3, $4, $5);

-- name: GetEventType :one
SELECT id, source, name, rule, schema, created_at, updated_at
FROM event_types WHERE id = $1;

-- name: ListEventTypes :many
SELECT id, source, name, rule, schema, created_at, updated_at
FROM event_types ORDER BY source, name;

-- name: EventTypesForSource :many
SELECT id, source, name, rule, schema, created_at, updated_at
FROM event_types WHERE source = $1 ORDER BY name;

-- name: UpdateEventType :execrows
UPDATE event_types SET name = $2, rule = $3, updated_at = now() WHERE id = $1;

-- name: DeleteEventType :execrows
DELETE FROM event_types WHERE id = $1;

-- name: LockEventTypeSource :exec
-- Serialises saves per source so two concurrent saves cannot each pass the
-- overlap check against a set that excludes the other.
SELECT pg_advisory_xact_lock(hashtext('event_types:' || sqlc.arg(source)::text));
-- name: InsertSource :exec
INSERT INTO sources (path, signing, secret) VALUES ($1, $2, $3);

-- name: GetSource :one
SELECT path, signing, secret, created_at, updated_at FROM sources WHERE path = $1;

-- name: ListSources :many
SELECT path, signing, secret, created_at, updated_at FROM sources ORDER BY created_at DESC, path;

-- name: SetSourceSigning :execrows
UPDATE sources SET signing = sqlc.arg(to_signing), updated_at = now()
WHERE path = sqlc.arg(path) AND signing = sqlc.arg(from_signing);

-- name: SetSourceSecret :execrows
UPDATE sources SET secret = $2, updated_at = now() WHERE path = $1;

-- name: SourceExists :one
SELECT EXISTS (SELECT 1 FROM sources WHERE path = $1);

-- name: DeriveSources :exec
SELECT derive_sources();
