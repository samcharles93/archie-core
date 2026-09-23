// Package legacyread reads an existing install's SQLite stores (the State
// Store's archie.db, the PocketBase event store and the Gateway conversation
// store) for the one-time import into Postgres, and verifies what was read.
//
// Every production opener of those files writes on open (schema migrations,
// PocketBase bootstrap, DDL and backfills), so this package never imports
// them: it opens each file read-only and issues only SELECT. It knows nothing
// about Postgres; values come back as the SQLite driver's native types
// (int64, float64, string, []byte, nil) under their legacy column names.
//
// The package exists only for the import and is deleted with it.
package legacyread

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Source is one legacy SQLite file opened read-only. The process that owns
// the file must be stopped: a read-only open cannot checkpoint a WAL.
type Source struct {
	db *sql.DB
}

// Open opens path read-only. It refuses a path that does not exist rather
// than letting SQLite create an empty database there.
func Open(ctx context.Context, path string) (*Source, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("legacyread: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("legacyread: open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("legacyread: open %s: %w", path, err), db.Close())
	}
	return &Source{db: db}, nil
}

// Close closes the source.
func (s *Source) Close() error { return s.db.Close() }

// Table names one legacy table: its columns as the production code leaves
// them (columns added by ALTER TABLE included), the columns that identify a
// row, and the order it is read in.
type Table struct {
	Name    string
	Columns []string
	Key     []string
	OrderBy string
}

// Rows is one table's contents in read order.
type Rows struct {
	Table Table
	Rows  [][]any
}

// Value returns column col of row i, or nil for an unknown column.
func (r Rows) Value(i int, col string) any {
	for j, c := range r.Table.Columns {
		if c == col {
			return r.Rows[i][j]
		}
	}
	return nil
}

// Keys returns each row's key, its key columns joined with "/", in read order.
func (r Rows) Keys() []string {
	keys := make([]string, len(r.Rows))
	for i := range r.Rows {
		parts := make([]string, len(r.Table.Key))
		for j, col := range r.Table.Key {
			parts[j] = fmt.Sprint(r.Value(i, col))
		}
		keys[i] = strings.Join(parts, "/")
	}
	return keys
}

// Read returns every row of t.
func (s *Source) Read(ctx context.Context, t Table) (Rows, error) {
	query := "SELECT " + strings.Join(t.Columns, ", ") + " FROM " + t.Name + " ORDER BY " + t.OrderBy
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return Rows{}, fmt.Errorf("legacyread: read %s: %w", t.Name, err)
	}
	defer func() { _ = rows.Close() }()
	out := Rows{Table: t}
	for rows.Next() {
		row := make([]any, len(t.Columns))
		ptrs := make([]any, len(row))
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Rows{}, fmt.Errorf("legacyread: read %s: %w", t.Name, err)
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Rows{}, fmt.Errorf("legacyread: read %s: %w", t.Name, err)
	}
	return out, nil
}

// IndexedMessageIDs returns the message ids the Gateway's FTS5 search index
// holds. messages_fts is an external-content index derived from messages, so
// it is rebuilt on the target rather than copied; what the import must account
// for is whether it covered every message, which comparing this set against
// the messages ids answers.
func (s *Source) IndexedMessageIDs(ctx context.Context) ([]string, error) {
	return s.strings(ctx, "SELECT CAST(id AS TEXT) FROM messages_fts_docsize ORDER BY id")
}

func (s *Source) strings(ctx context.Context, query string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("legacyread: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("legacyread: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
