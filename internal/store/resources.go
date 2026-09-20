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
 request_id TEXT NOT NULL UNIQUE, expected_version INTEGER NOT NULL,
 current_version INTEGER NOT NULL, at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_resource_history_kind_version ON resource_history(kind, version);
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

func (s *Store) PutResource(ctx context.Context, w ResourceWrite) (_ Resource, retErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Resource{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, ok, err := resourceByRequest(ctx, tx, w.RequestID); err != nil {
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

func resourceByRequest(ctx context.Context, tx *sql.Tx, requestID string) (Resource, bool, error) {
	var r Resource
	err := tx.QueryRowContext(ctx, `SELECT kind,value,version,actor,source,request_id,expected_version,current_version,at FROM resource_history WHERE request_id=?`, requestID).
		Scan(&r.Kind, &r.Value, &r.Version, &r.Actor, &r.Source, &r.RequestID, &r.ExpectedVersion, &r.CurrentVersion, sqliteTime{&r.At})
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, false, nil
	}
	return r, err == nil, err
}
