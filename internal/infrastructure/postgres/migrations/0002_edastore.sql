-- EDA schema: captures, mappings, bindings, the two dispatch ledgers and
-- tool_calls. This is the Postgres replacement for the PocketBase-owned
-- collections in internal/infrastructure/edastore.
--
-- Translations from the PocketBase/SQLite schema this replaces, and why:
--   * Record ids stay text. PocketBase issued a random 15-character id and the
--     wire carries binding/capture/mapping ids as opaque strings; the adapter
--     mints a random id in Go, so nothing changes for consumers.
--   * Times become timestamptz, matching the State Store migration. The
--     SQLite store formatted captures.received_at as fixed-width text so
--     lexicographic order matched chronological order (PocketBase autodates
--     are only millisecond-precise); Postgres orders timestamptz natively, so
--     that normalisation does not exist here. This loses sub-microsecond
--     precision, which the State Store migration already accepted for event
--     timestamps.
--   * Every column behind a Go int is bigint. binding_version and task_id are
--     int64 on the wire; tool_calls.attempt and duration_ms are Go ints.
--   * bindings.mapping is a plain text column rather than a real foreign key:
--     the PocketBase Relation field was not enforced by SQLite (foreign_keys
--     is off; PocketBase resolves relations in app code), and the store
--     accepts an empty mapping id on insert. A real FK would reject those rows.
--   * bindings.secret stays text with no length bound: the cipher envelope is
--     base64url and must never be truncated or normalised, or Decrypt fails.

-- +goose Up

CREATE TABLE captures (
	id            text PRIMARY KEY,
	source        text NOT NULL,
	remote_addr   text NOT NULL DEFAULT '',
	content_type  text NOT NULL DEFAULT '',
	headers       text NOT NULL DEFAULT '',
	body          text NOT NULL DEFAULT '',
	authenticated boolean NOT NULL DEFAULT false,
	received_at   timestamptz NOT NULL
);
CREATE INDEX idx_captures_source ON captures (source, received_at);

CREATE TABLE mappings (
	id          text PRIMARY KEY,
	name        text NOT NULL,
	source_hint text NOT NULL DEFAULT '',
	fields      text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE bindings (
	id         text PRIMARY KEY,
	name       text NOT NULL,
	source     text NOT NULL,
	mapping    text NOT NULL DEFAULT '',
	workflow   text NOT NULL DEFAULT '',
	owner      text NOT NULL DEFAULT '',
	repo       text NOT NULL DEFAULT '',
	version    bigint NOT NULL DEFAULT 0,
	status     text NOT NULL DEFAULT 'pending_approval',
	secret     text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_bindings_source_status ON bindings (source, status);
-- "One binding per source" is now schema-enforced rather than read-then-refuse
-- in the store: a concurrent second insert for a source is refused by the
-- database (SQLSTATE 23505), which the store maps to ErrBindingOverlap.
CREATE UNIQUE INDEX idx_bindings_source ON bindings (source);
-- "One armed binding per source" is a distinct rule. The full unique above
-- already implies it, but the partial index keeps the invariant explicit and
-- durable if the per-source rule is ever relaxed to allow multiple matchers
-- while still forbidding two armed ones.
CREATE UNIQUE INDEX idx_bindings_armed_source ON bindings (source) WHERE status = 'armed';

CREATE TABLE binding_dispatches (
	binding         text NOT NULL,
	binding_version bigint NOT NULL DEFAULT 0,
	capture         text NOT NULL,
	task_id         bigint NOT NULL DEFAULT 0,
	dispatched_at   timestamptz NOT NULL DEFAULT now()
);
-- At-most-once: binding_version is stored but deliberately not part of the key,
-- so a version bump does not permit re-dispatching the same (binding, capture).
CREATE UNIQUE INDEX idx_binding_dispatch_once ON binding_dispatches (binding, capture);

CREATE TABLE playbook_dispatches (
	playbook_id      text NOT NULL,
	playbook_version text NOT NULL,
	event_id         text NOT NULL,
	action_id        text NOT NULL,
	dispatched_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_playbook_dispatch_once ON playbook_dispatches
	(playbook_id, playbook_version, event_id, action_id);

CREATE TABLE tool_calls (
	id          text PRIMARY KEY,
	task_id     bigint NOT NULL DEFAULT 0,
	attempt     bigint NOT NULL DEFAULT 0,
	tool        text NOT NULL,
	args        text NOT NULL DEFAULT '',
	result      text NOT NULL DEFAULT '',
	error       text NOT NULL DEFAULT '',
	duration_ms bigint NOT NULL DEFAULT 0,
	called_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_tool_calls_task ON tool_calls (task_id, attempt);

-- +goose Down

DROP TABLE tool_calls;
DROP TABLE playbook_dispatches;
DROP TABLE binding_dispatches;
DROP TABLE bindings;
DROP TABLE mappings;
DROP TABLE captures;
