package store

import (
	"context"
	"fmt"
	"time"
)

const channelStatusSchema = `
CREATE TABLE IF NOT EXISTS channel_status (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL DEFAULT '',
	state            TEXT NOT NULL DEFAULT '',
	detail           TEXT NOT NULL DEFAULT '',
	configured       INTEGER NOT NULL DEFAULT 0,
	reload_supported INTEGER NOT NULL DEFAULT 0,
	observed_at      TEXT NOT NULL
);
`

// PutChannelStatus records the reporting process's whole set, in one
// transaction: rows it no longer reports are deleted, so a channel that stopped
// or was removed does not linger as a stale "running" the dashboard would show
// indefinitely.
func (s *Store) PutChannelStatus(ctx context.Context, channels []ChannelStatus) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: put channel status: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	reported := make([]any, 0, len(channels)+1)
	placeholders := make([]byte, 0, len(channels)*2)
	for index, channel := range channels {
		if channel.ID == "" {
			return fmt.Errorf("store: put channel status: a channel needs an id")
		}
		if index > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		reported = append(reported, channel.ID)

		observed := channel.ObservedAt
		if observed.IsZero() {
			observed = time.Now()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO channel_status (id, name, state, detail, configured, reload_supported, observed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				name=excluded.name,
				state=excluded.state,
				detail=excluded.detail,
				configured=excluded.configured,
				reload_supported=excluded.reload_supported,
				observed_at=excluded.observed_at`,
			channel.ID, channel.Name, channel.State, channel.Detail,
			channel.Configured, channel.ReloadSupported, observed.UTC().Format(configSnapshotTimeLayout)); err != nil {
			return fmt.Errorf("store: put channel status %s: %w", channel.ID, err)
		}
	}

	// An empty report is a real report -- the process hosts no channels -- and it
	// clears the table rather than leaving the previous set standing.
	query := `DELETE FROM channel_status`
	if len(reported) > 0 {
		query += ` WHERE id NOT IN (` + string(placeholders) + `)`
	}
	if _, err := tx.ExecContext(ctx, query, reported...); err != nil {
		return fmt.Errorf("store: put channel status: prune: %w", err)
	}
	return tx.Commit()
}

// ChannelStatus returns every reported channel, ordered by id so a reader and a
// test see the same order twice.
func (s *Store) ChannelStatus(ctx context.Context) ([]ChannelStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, state, detail, configured, reload_supported, observed_at
		FROM channel_status ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list channel status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var channels []ChannelStatus
	for rows.Next() {
		var (
			channel  ChannelStatus
			observed string
		)
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.State, &channel.Detail,
			&channel.Configured, &channel.ReloadSupported, &observed); err != nil {
			return nil, fmt.Errorf("store: scan channel status: %w", err)
		}
		if at, err := time.Parse(configSnapshotTimeLayout, observed); err == nil {
			channel.ObservedAt = at
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list channel status: %w", err)
	}
	return channels, nil
}
