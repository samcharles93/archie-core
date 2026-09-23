package legacyread

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// HasData reports whether the legacy file at path holds any row in tables. A
// missing file, and a table the file never created, hold nothing. It is the
// legacy half of the boot gate: a Postgres database without a completed import
// must not serve while this is true for any source.
func HasData(ctx context.Context, path string, tables []Table) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	src, err := Open(ctx, path)
	if err != nil {
		return false, err
	}
	defer func() { _ = src.Close() }()
	for _, t := range tables {
		var present bool
		if err := src.db.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", t.Name).Scan(&present); err != nil {
			return false, fmt.Errorf("legacyread: %s: %w", path, err)
		}
		if !present {
			continue
		}
		var rows bool
		if err := src.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM "+t.Name+")").Scan(&rows); err != nil {
			return false, fmt.Errorf("legacyread: %s: %s: %w", path, t.Name, err)
		}
		if rows {
			return true, nil
		}
	}
	return false, nil
}

// SequenceOf returns the state of table's AUTOINCREMENT id column.
func (s *Source) SequenceOf(ctx context.Context, table string) (Sequence, error) {
	seq := Sequence{Table: table}
	var high int64
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT COALESCE((SELECT max(id) FROM %[1]s), 0), COALESCE((SELECT seq FROM sqlite_sequence WHERE name = '%[1]s'), 0)",
		table)).Scan(&seq.MaxID, &high)
	if err != nil {
		return Sequence{}, fmt.Errorf("legacyread: sequence %s: %w", table, err)
	}
	seq.Next = max(seq.MaxID, high) + 1
	return seq, nil
}
