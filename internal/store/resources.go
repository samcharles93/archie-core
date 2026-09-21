package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrResourceNotFound        = errors.New("resource not found")
	ErrResourceVersionConflict = errors.New("resource version conflict")
)

type Resource struct {
	Kind            string
	Value           []byte
	Version         int64
	Actor           string
	Source          string
	RequestID       string
	ExpectedVersion int64
	CurrentVersion  int64
	At              time.Time
}

type ResourceWrite struct {
	Kind            string
	Value           []byte
	Actor           string
	Source          string
	RequestID       string
	ExpectedVersion int64
	At              time.Time
}

const resourcesSchema = `
CREATE TABLE IF NOT EXISTS resources (
 kind TEXT PRIMARY KEY, value BLOB NOT NULL, version INTEGER NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS resource_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, value BLOB NOT NULL,
 version INTEGER NOT NULL, actor TEXT NOT NULL, source TEXT NOT NULL,
 request_id TEXT NOT NULL, expected_version INTEGER NOT NULL,
 current_version INTEGER NOT NULL, at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_resource_history_kind_version ON resource_history(kind, version);
-- The request-ID idempotency key is per kind, not global: a resource write
-- replays only the same kind's prior write (resourceByRequest). A database
-- written before v4 carries the old global UNIQUE as a column constraint;
-- migrateResourceHistoryToPerKindRequestIDs rebuilds it to this shape.
CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_history_kind_request ON resource_history(kind, request_id);
`

func (s *Store) Resource(ctx context.Context, kind string) (Resource, error) {
	var r Resource
	err := s.db.QueryRowContext(ctx, `SELECT kind,value,version,updated_at FROM resources WHERE kind=?`, kind).
		Scan(&r.Kind, &r.Value, &r.Version, sqliteTime{&r.At})
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrResourceNotFound
	}
	return r, err
}

// ResourceHistory returns kind's revisions newest first, each carrying the
// audit record written with it. limit caps the rows; zero or less returns
// every revision. An unknown kind has no history, which is not an error.
func (s *Store) ResourceHistory(ctx context.Context, kind string, limit int) ([]Resource, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind,value,version,actor,source,request_id,expected_version,current_version,at FROM resource_history WHERE kind=? ORDER BY version DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var history []Resource
	for rows.Next() {
		var r Resource
		if err := rows.Scan(&r.Kind, &r.Value, &r.Version, &r.Actor, &r.Source, &r.RequestID, &r.ExpectedVersion, &r.CurrentVersion, sqliteTime{&r.At}); err != nil {
			return nil, err
		}
		history = append(history, r)
	}
	return history, rows.Err()
}

func (s *Store) PutResource(ctx context.Context, w ResourceWrite) (_ Resource, retErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Resource{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, ok, err := resourceByRequest(ctx, tx, w.Kind, w.RequestID); err != nil {
		return Resource{}, err
	} else if ok {
		return existing, nil
	}
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT version FROM resources WHERE kind=?`, w.Kind).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Resource{}, err
	}
	if current != w.ExpectedVersion {
		return Resource{}, fmt.Errorf("%w: expected %d, current %d", ErrResourceVersionConflict, w.ExpectedVersion, current)
	}
	if w.At.IsZero() {
		w.At = time.Now().UTC()
	}
	next := current + 1
	at := w.At.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO resources(kind,value,version,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind) DO UPDATE SET value=excluded.value,version=excluded.version,updated_at=excluded.updated_at`, w.Kind, w.Value, next, at); err != nil {
		return Resource{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO resource_history(kind,value,version,actor,source,request_id,expected_version,current_version,at) VALUES(?,?,?,?,?,?,?,?,?)`, w.Kind, w.Value, next, w.Actor, w.Source, w.RequestID, w.ExpectedVersion, current, at); err != nil {
		return Resource{}, err
	}
	if err := tx.Commit(); err != nil {
		return Resource{}, err
	}
	return Resource{Kind: w.Kind, Value: append([]byte(nil), w.Value...), Version: next, Actor: w.Actor, Source: w.Source, RequestID: w.RequestID, ExpectedVersion: w.ExpectedVersion, CurrentVersion: current, At: w.At.UTC()}, nil
}

// resourceByRequest replays one prior write by its request ID, so a retried
// replace is idempotent instead of a second revision. The key is (kind,
// request ID), not the request ID alone: the API is per kind, so an ID reused
// across kinds must write the new kind rather than silently replay the first
// kind's resource (archie-core-fcvd).
func resourceByRequest(ctx context.Context, tx *sql.Tx, kind, requestID string) (Resource, bool, error) {
	var r Resource
	err := tx.QueryRowContext(ctx, `SELECT kind,value,version,actor,source,request_id,expected_version,current_version,at FROM resource_history WHERE kind=? AND request_id=?`, kind, requestID).
		Scan(&r.Kind, &r.Value, &r.Version, &r.Actor, &r.Source, &r.RequestID, &r.ExpectedVersion, &r.CurrentVersion, sqliteTime{&r.At})
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, false, nil
	}
	return r, err == nil, err
}
