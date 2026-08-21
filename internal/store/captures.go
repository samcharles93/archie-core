package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const capturesSchema = `
CREATE TABLE IF NOT EXISTS captured_events (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	received_at   TEXT NOT NULL,
	source        TEXT NOT NULL DEFAULT '',
	remote_addr   TEXT NOT NULL DEFAULT '',
	content_type  TEXT NOT NULL DEFAULT '',
	headers       TEXT NOT NULL DEFAULT '{}',
	body          TEXT NOT NULL DEFAULT '',
	authenticated INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_captured_events_source ON captured_events(source, id);
`

// CapturedEvent is one unbound inbound webhook capture: no workflow binding,
// no forge/task association -- just what arrived, from where, and when.
// See docs/prds/event-capture-storage.md.
type CapturedEvent struct {
	ID          int64     `json:"id"`
	ReceivedAt  time.Time `json:"received_at"`
	Source      string    `json:"source"`
	RemoteAddr  string    `json:"remote_addr"`
	ContentType string    `json:"content_type"`
	// Headers and Body are redacted (webhookguard.RedactPayload) before
	// they ever reach InsertCapture; the store persists what it is given.
	Headers       string `json:"headers"`
	Body          string `json:"body"`
	Authenticated bool   `json:"authenticated"`
}

// captureTimeLayout is fixed-width (unlike time.RFC3339Nano, whose
// fractional-seconds field is trimmed of trailing zeros and omitted
// entirely when zero). A variable-width fractional part breaks lexicographic
// string comparison -- "15:04:05Z" (an exact second) sorts AFTER
// "15:04:05.1Z" (0.1s later) because 'Z' > '.', which would make the
// retention prune's `WHERE received_at < ?` unreliable. Second precision is
// enough here: row order already comes from id (autoincrement), and
// retention windows are measured in days, not sub-second intervals.
const captureTimeLayout = time.RFC3339

// InsertCapture persists a captured event and prunes the table in the same
// transaction: rows older than retention (when > 0) and rows beyond the
// newest maxEvents (when > 0) are deleted. Either bound of 0 or less
// disables that prune. See docs/prds/event-capture-storage.md's disk-bound
// mechanics -- this is one of three independent bounds, the other two
// (per-request size cap, per-source rate limit) are enforced by the caller
// before the body ever reaches this method.
func (s *Store) InsertCapture(ctx context.Context, c CapturedEvent, retention time.Duration, maxEvents int) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if c.ReceivedAt.IsZero() {
		c.ReceivedAt = time.Now().UTC()
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO captured_events (received_at, source, remote_addr, content_type, headers, body, authenticated)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ReceivedAt.UTC().Format(captureTimeLayout), c.Source, c.RemoteAddr,
		c.ContentType, c.Headers, c.Body, c.Authenticated)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	if retention > 0 {
		cutoff := time.Now().UTC().Add(-retention).Format(captureTimeLayout)
		if _, err := tx.ExecContext(ctx, `DELETE FROM captured_events WHERE received_at < ?`, cutoff); err != nil {
			return 0, err
		}
	}
	if maxEvents > 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM captured_events WHERE id NOT IN (
				SELECT id FROM captured_events ORDER BY id DESC LIMIT ?
			)`, maxEvents); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// ListCaptures returns up to limit captured events, newest first.
func (s *Store) ListCaptures(ctx context.Context, limit int) (out []CapturedEvent, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, received_at, source, remote_addr, content_type, headers, body, authenticated
		FROM captured_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()

	for rows.Next() {
		var c CapturedEvent
		var receivedAt string
		var authenticated int
		if err := rows.Scan(&c.ID, &receivedAt, &c.Source, &c.RemoteAddr,
			&c.ContentType, &c.Headers, &c.Body, &authenticated); err != nil {
			return nil, err
		}
		parsed, err := parseCaptureTime(receivedAt)
		if err != nil {
			return nil, err
		}
		c.ReceivedAt = parsed
		c.Authenticated = authenticated != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func parseCaptureTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("store: unrecognised capture timestamp %q", v)
}
