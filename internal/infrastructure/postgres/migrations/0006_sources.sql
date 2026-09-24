-- Capture sources own signing (docs/prds/event-automation.md "Sources").
--
--   * path is the key. It is the URL segment a sender posts to and the string
--     captures.source and bindings.source already carry, so existing rows keep
--     resolving without a rewrite. It never changes after creation.
--   * signing is signed by default. Turning it off is a request
--     (unsigned_pending_approval) that only an approval makes effective.
--   * secret holds the cipher envelope when a bindings encryption key is
--     configured, plaintext otherwise, exactly as bindings.secret did.
--   * captures.unsigned marks an event taken on an approved unsigned source.
--   * bindings.secret is no longer read. It stays so the one-time legacy
--     import can load it and derive_sources can move it onto the source.
--
-- derive_sources creates a source for every source string bindings and
-- captures already use. A bound source takes its binding's secret (one binding
-- per source is schema-enforced), so a signed sender keeps authenticating. A
-- capture-only source gets no secret and dispatches nothing until the
-- operator generates one. The legacy import calls it again after loading.

-- +goose Up

CREATE TABLE sources (
	path       text PRIMARY KEY,
	signing    text NOT NULL DEFAULT 'signed'
		CHECK (signing IN ('signed', 'unsigned_pending_approval', 'unsigned')),
	secret     text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE captures ADD COLUMN unsigned boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
CREATE FUNCTION derive_sources() RETURNS void LANGUAGE sql AS $$
	INSERT INTO sources (path, secret)
	SELECT source, secret FROM bindings WHERE source <> ''
	ON CONFLICT (path) DO NOTHING;
	INSERT INTO sources (path)
	SELECT DISTINCT source FROM captures WHERE source <> ''
	ON CONFLICT (path) DO NOTHING;
$$;
-- +goose StatementEnd

SELECT derive_sources();

-- +goose Down

DROP FUNCTION derive_sources();
ALTER TABLE captures DROP COLUMN unsigned;
DROP TABLE sources;
