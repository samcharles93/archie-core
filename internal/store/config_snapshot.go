package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const configSnapshotSchema = `
CREATE TABLE IF NOT EXISTS config_snapshot (
	id           INTEGER PRIMARY KEY CHECK (id = 1),
	schema       TEXT NOT NULL DEFAULT '',
	document     TEXT NOT NULL DEFAULT '',
	published_at TEXT NOT NULL
);
`

// configSnapshotTimeLayout follows the rest of the store's timestamp columns:
// fixed-width, so string and chronological order agree.
const configSnapshotTimeLayout = time.RFC3339

// PutConfigSnapshot replaces the published snapshot. One running
// configuration means one row, so a republish overwrites rather than
// appending a history nothing reads.
func (s *Store) PutConfigSnapshot(ctx context.Context, snapshot ConfigSnapshot) error {
	published := snapshot.PublishedAt
	if published.IsZero() {
		published = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO config_snapshot (id, schema, document, published_at)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			schema=excluded.schema,
			document=excluded.document,
			published_at=excluded.published_at`,
		snapshot.Schema, string(snapshot.Document), published.UTC().Format(configSnapshotTimeLayout))
	if err != nil {
		return fmt.Errorf("store: put config snapshot: %w", err)
	}
	return nil
}

// ConfigSnapshot returns the published snapshot. found is false, with a nil
// error, before anything has published one -- the normal state of a store
// whose daemon has not booted yet.
func (s *Store) ConfigSnapshot(ctx context.Context) (ConfigSnapshot, bool, error) {
	var (
		snapshot  ConfigSnapshot
		document  string
		published string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT schema, document, published_at FROM config_snapshot WHERE id = 1`).
		Scan(&snapshot.Schema, &document, &published)
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigSnapshot{}, false, nil
	}
	if err != nil {
		return ConfigSnapshot{}, false, fmt.Errorf("store: read config snapshot: %w", err)
	}
	snapshot.Document = []byte(document)
	if at, err := time.Parse(configSnapshotTimeLayout, published); err == nil {
		snapshot.PublishedAt = at
	}
	return snapshot, true, nil
}
