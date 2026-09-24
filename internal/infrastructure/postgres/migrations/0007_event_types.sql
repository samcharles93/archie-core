-- Event types: named kinds of event from one source, each defined by a match
-- rule (docs/prds/event-automation.md, "Event types"). rule and schema are the
-- JSON encodings of eventtype.Rule and the inferred path-to-type schema; the
-- store evaluates them in Go, so they are opaque text here.
--
-- captures.event_type records the type a capture was identified as when it
-- arrived. Empty means unidentified, and an unidentified capture is never
-- listed for dispatch. It is a plain text column, not a foreign key: deleting
-- a type must not rewrite what past captures were identified as.

-- +goose Up

CREATE TABLE event_types (
	id         text PRIMARY KEY,
	source     text NOT NULL,
	name       text NOT NULL,
	rule       text NOT NULL,
	schema     text NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_event_types_source_name ON event_types (source, name);

ALTER TABLE captures ADD COLUMN event_type text NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE captures DROP COLUMN event_type;
DROP TABLE event_types;
