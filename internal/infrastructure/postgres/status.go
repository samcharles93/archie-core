package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// PutConfigSnapshot replaces the published snapshot (one row, id = 1).
func (s *Store) PutConfigSnapshot(ctx context.Context, snapshot storecontract.ConfigSnapshot) error {
	published := snapshot.PublishedAt
	if published.IsZero() {
		published = time.Now()
	}
	if err := s.queries().UpsertConfigSnapshot(ctx, postgresdb.UpsertConfigSnapshotParams{
		Schema: snapshot.Schema, Document: string(snapshot.Document), PublishedAt: published.UTC(),
	}); err != nil {
		return fmt.Errorf("store: put config snapshot: %w", err)
	}
	return nil
}

// ConfigSnapshot returns the published snapshot. found is false, with a nil
// error, before anything has published one.
func (s *Store) ConfigSnapshot(ctx context.Context) (storecontract.ConfigSnapshot, bool, error) {
	row, err := s.queries().ConfigSnapshotByID(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.ConfigSnapshot{}, false, nil
	}
	if err != nil {
		return storecontract.ConfigSnapshot{}, false, fmt.Errorf("store: read config snapshot: %w", err)
	}
	return storecontract.ConfigSnapshot{
		Schema: row.Schema, Document: []byte(row.Document), PublishedAt: row.PublishedAt,
	}, true, nil
}

// PutChannelStatus records the reporting process's whole set in one
// transaction, removing rows it no longer reports.
func (s *Store) PutChannelStatus(ctx context.Context, channels []storecontract.ChannelStatus) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: put channel status: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	reported := make([]string, 0, len(channels))
	for _, channel := range channels {
		if channel.ID == "" {
			return fmt.Errorf("store: put channel status: a channel needs an id")
		}
		observed := channel.ObservedAt
		if observed.IsZero() {
			observed = time.Now()
		}
		if err := q.UpsertChannelStatus(ctx, postgresdb.UpsertChannelStatusParams{
			ID: channel.ID, Name: channel.Name, State: channel.State, Detail: channel.Detail,
			Configured: channel.Configured, ReloadSupported: channel.ReloadSupported,
			ObservedAt: observed.UTC(),
		}); err != nil {
			return fmt.Errorf("store: put channel status %s: %w", channel.ID, err)
		}
		reported = append(reported, channel.ID)
	}

	if err := q.DeleteChannelStatusNotIn(ctx, reported); err != nil {
		return fmt.Errorf("store: put channel status: prune: %w", err)
	}
	return tx.Commit(ctx)
}

// ChannelStatus returns every reported channel, ordered by id.
func (s *Store) ChannelStatus(ctx context.Context) ([]storecontract.ChannelStatus, error) {
	rows, err := s.queries().ListChannelStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list channel status: %w", err)
	}
	out := make([]storecontract.ChannelStatus, 0, len(rows))
	for _, r := range rows {
		out = append(out, storecontract.ChannelStatus{
			ID: r.ID, Name: r.Name, State: r.State, Detail: r.Detail,
			Configured: r.Configured, ReloadSupported: r.ReloadSupported, ObservedAt: r.ObservedAt,
		})
	}
	return out, nil
}

// PutApplyStatus records what one process applied for one resource kind,
// replacing its own (process, kind) row.
func (s *Store) PutApplyStatus(ctx context.Context, status storecontract.ApplyStatus) error {
	reported := status.ReportedAt
	if reported.IsZero() {
		reported = time.Now()
	}
	if err := s.queries().UpsertApplyStatus(ctx, postgresdb.UpsertApplyStatusParams{
		Process: status.Process, Kind: status.Kind, AppliedVersion: status.AppliedVersion,
		Error: status.Error, ReportedAt: reported.UTC(),
	}); err != nil {
		return fmt.Errorf("store: put apply status: %w", err)
	}
	return nil
}

// ListApplyStatus returns every process's report, ordered by (process, kind).
func (s *Store) ListApplyStatus(ctx context.Context) ([]storecontract.ApplyStatus, error) {
	rows, err := s.queries().ListApplyStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list apply status: %w", err)
	}
	out := make([]storecontract.ApplyStatus, 0, len(rows))
	for _, r := range rows {
		out = append(out, storecontract.ApplyStatus{
			Process: r.Process, Kind: r.Kind, AppliedVersion: r.AppliedVersion,
			Error: r.Error, ReportedAt: r.ReportedAt,
		})
	}
	return out, nil
}
