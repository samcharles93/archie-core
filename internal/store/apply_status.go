package store

import (
	"context"
	"fmt"
	"time"
)

const applyStatusSchema = `
CREATE TABLE IF NOT EXISTS apply_status (
	process         TEXT NOT NULL,
	kind            TEXT NOT NULL,
	applied_version INTEGER NOT NULL DEFAULT 0,
	error           TEXT NOT NULL DEFAULT '',
	reported_at     TEXT NOT NULL,
	PRIMARY KEY (process, kind)
);
`

// applyStatusTimeLayout matches the rest of the store's timestamp columns.
const applyStatusTimeLayout = time.RFC3339

// PutApplyStatus records what one process applied for one resource kind. The
// key is (process, kind) and a report replaces its own row: a process
// re-stamps on an interval, and a history of re-stamps is not something any
// reader wants.
func (s *Store) PutApplyStatus(ctx context.Context, status ApplyStatus) error {
	reported := status.ReportedAt
	if reported.IsZero() {
		reported = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO apply_status (process, kind, applied_version, error, reported_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(process, kind) DO UPDATE SET
			applied_version=excluded.applied_version,
			error=excluded.error,
			reported_at=excluded.reported_at`,
		status.Process, status.Kind, status.AppliedVersion, status.Error,
		reported.UTC().Format(applyStatusTimeLayout))
	if err != nil {
		return fmt.Errorf("store: put apply status: %w", err)
	}
	return nil
}

// ListApplyStatus returns every process's report. An empty slice is the
// normal state of a store whose processes have not booted yet, not an error.
func (s *Store) ListApplyStatus(ctx context.Context) ([]ApplyStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT process, kind, applied_version, error, reported_at
		FROM apply_status ORDER BY process, kind`)
	if err != nil {
		return nil, fmt.Errorf("store: list apply status: %w", err)
	}
	defer rows.Close()

	statuses := []ApplyStatus{}
	for rows.Next() {
		var (
			status   ApplyStatus
			reported string
		)
		if err := rows.Scan(&status.Process, &status.Kind, &status.AppliedVersion, &status.Error, &reported); err != nil {
			return nil, fmt.Errorf("store: scan apply status: %w", err)
		}
		status.ReportedAt, err = time.Parse(applyStatusTimeLayout, reported)
		if err != nil {
			return nil, fmt.Errorf("store: parse apply status time: %w", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list apply status: %w", err)
	}
	return statuses, nil
}
