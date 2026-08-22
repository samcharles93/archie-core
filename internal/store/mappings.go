package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

const mappingsSchema = `
CREATE TABLE IF NOT EXISTS field_mappings (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL DEFAULT '',
	source_hint TEXT NOT NULL DEFAULT '',
	fields_json TEXT NOT NULL DEFAULT '[]',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
`

// ErrMappingNotFound is returned by UpdateMapping and DeleteMapping when no
// row matches the given ID. GetMapping uses (nil, nil) instead, matching
// TaskByID's convention -- "not found" is the normal answer to "does a
// mapping with this ID currently exist," not an error.
var ErrMappingNotFound = errors.New("store: mapping not found")

// mappingTimeLayout follows captureTimeLayout's reasoning: fixed-width so
// string and chronological order agree, even though nothing here prunes on
// a string comparison today -- consistency with the rest of the store's
// timestamp columns.
const mappingTimeLayout = time.RFC3339

// InsertMapping persists a new mapping and stamps CreatedAt/UpdatedAt to
// now, ignoring any caller-supplied values.
func (s *Store) InsertMapping(ctx context.Context, m mapping.Mapping) (int64, error) {
	fieldsJSON, err := json.Marshal(m.Fields)
	if err != nil {
		return 0, fmt.Errorf("store: marshal mapping fields: %w", err)
	}
	now := time.Now().UTC().Format(mappingTimeLayout)
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO field_mappings (name, source_hint, fields_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		m.Name, m.SourceHint, string(fieldsJSON), now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetMapping returns the mapping with the given ID, or (nil, nil) if none
// exists.
func (s *Store) GetMapping(ctx context.Context, id int64) (*mapping.Mapping, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, source_hint, fields_json, created_at, updated_at
		FROM field_mappings WHERE id=?`, id)
	m, err := scanMapping(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ListMappings returns every mapping, newest first.
func (s *Store) ListMappings(ctx context.Context) (out []mapping.Mapping, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, source_hint, fields_json, created_at, updated_at
		FROM field_mappings ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()

	for rows.Next() {
		m, err := scanMappingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// UpdateMapping replaces the name, source hint and fields of an existing
// mapping and bumps UpdatedAt to now. Returns ErrMappingNotFound if m.ID
// does not exist.
func (s *Store) UpdateMapping(ctx context.Context, m mapping.Mapping) error {
	fieldsJSON, err := json.Marshal(m.Fields)
	if err != nil {
		return fmt.Errorf("store: marshal mapping fields: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE field_mappings SET name=?, source_hint=?, fields_json=?, updated_at=?
		WHERE id=?`,
		m.Name, m.SourceHint, string(fieldsJSON), time.Now().UTC().Format(mappingTimeLayout), m.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMappingNotFound
	}
	return nil
}

// DeleteMapping removes a mapping by ID. Returns ErrMappingNotFound if it
// does not exist.
func (s *Store) DeleteMapping(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM field_mappings WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMappingNotFound
	}
	return nil
}

// mappingScanner is satisfied by both *sql.Row and *sql.Rows, so
// scanMapping's column list stays in exactly one place.
type mappingScanner interface {
	Scan(dest ...any) error
}

func scanMapping(row mappingScanner) (*mapping.Mapping, error) {
	return scanMappingRow(row)
}

func scanMappingRow(row mappingScanner) (*mapping.Mapping, error) {
	var m mapping.Mapping
	var fieldsJSON, createdAt, updatedAt string
	if err := row.Scan(&m.ID, &m.Name, &m.SourceHint, &fieldsJSON, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(fieldsJSON), &m.Fields); err != nil {
		return nil, fmt.Errorf("store: unmarshal mapping fields: %w", err)
	}
	created, err := time.Parse(mappingTimeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("store: unrecognised mapping created_at %q: %w", createdAt, err)
	}
	updated, err := time.Parse(mappingTimeLayout, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("store: unrecognised mapping updated_at %q: %w", updatedAt, err)
	}
	m.CreatedAt = created.UTC()
	m.UpdatedAt = updated.UTC()
	return &m, nil
}
