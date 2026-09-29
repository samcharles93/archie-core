package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// PackageContributions records the control-plane resource entries an org's
// installed package projected. Removal consults this ledger to take exactly
// the package's own contributions back out, so a remove that fails midway
// keeps the ledger for the next attempt and another package's entries stay.
type PackageContributions struct{ pool *pgxpool.Pool }

var _ storepkg.ProjectionLedger = (*PackageContributions)(nil)

func NewPackageContributions(pool *pgxpool.Pool) *PackageContributions {
	return &PackageContributions{pool: pool}
}

func (s *PackageContributions) Record(ctx context.Context, orgID, name string, entries []storepkg.ProjectionEntry) error {
	if orgID == "" || name == "" {
		return errors.New("org and package name are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := postgresdb.New(tx)
	if err := queries.LockInstalledPackageContributions(ctx, postgresdb.LockInstalledPackageContributionsParams{Column1: orgID, Column2: name}); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := queries.InsertInstalledPackageContribution(ctx, postgresdb.InsertInstalledPackageContributionParams{
			OrgID: orgID, PackageName: name, Family: entry.Family, EntryID: entry.EntryID,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PackageContributions) Entries(ctx context.Context, orgID, name string) ([]storepkg.ProjectionEntry, error) {
	rows, err := postgresdb.New(s.pool).ListInstalledPackageContributions(ctx, postgresdb.ListInstalledPackageContributionsParams{OrgID: orgID, PackageName: name})
	if err != nil {
		return nil, err
	}
	entries := make([]storepkg.ProjectionEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, storepkg.ProjectionEntry{Family: row.Family, EntryID: row.EntryID})
	}
	return entries, nil
}

func (s *PackageContributions) Forget(ctx context.Context, orgID, name, family string) error {
	return postgresdb.New(s.pool).DeleteInstalledPackageContributions(ctx, postgresdb.DeleteInstalledPackageContributionsParams{
		OrgID: orgID, PackageName: name, Family: family,
	})
}
