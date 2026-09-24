-- Mappings per event type, match counts and several bindings per source
-- (docs/prds/event-automation.md, "Mappings" and "Bindings").
--
-- mappings.event_type is the event type a mapping belongs to; like
-- captures.event_type it is plain text, not a foreign key. A binding applies
-- to its mapping's event type, so any number of bindings may share a source
-- and the two per-source unique indexes are dropped.
--
-- mapping_matches holds one row per (mapping, capture) the mapping resolved;
-- a mapping's matched-event count and last match are read from it.
-- bindings.filter is an optional CEL expression over the mapping's parameters.

-- +goose Up

ALTER TABLE mappings ADD COLUMN event_type text NOT NULL DEFAULT '';
ALTER TABLE bindings ADD COLUMN filter text NOT NULL DEFAULT '';

DROP INDEX idx_bindings_source;
DROP INDEX idx_bindings_armed_source;

CREATE TABLE mapping_matches (
	mapping    text NOT NULL,
	capture    text NOT NULL,
	matched_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (mapping, capture)
);

-- +goose Down

DROP TABLE mapping_matches;
CREATE UNIQUE INDEX idx_bindings_armed_source ON bindings (source) WHERE status = 'armed';
CREATE UNIQUE INDEX idx_bindings_source ON bindings (source);
ALTER TABLE bindings DROP COLUMN filter;
ALTER TABLE mappings DROP COLUMN event_type;
