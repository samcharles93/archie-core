package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// InstalledPackages persists org-scoped Archie package pins.
type InstalledPackages struct{ pool *pgxpool.Pool }

var _ storepkg.Repository = (*InstalledPackages)(nil)

func NewInstalledPackages(pool *pgxpool.Pool) *InstalledPackages {
	return &InstalledPackages{pool: pool}
}

func (s *InstalledPackages) Install(ctx context.Context, p storepkg.Installed) error {
	if p.OrgID == "" || p.Name == "" || p.Reference == "" || p.Digest == "" {
		return errors.New("installed package identity and pin are required")
	}
	if err := p.Descriptor.Validate(); err != nil {
		return err
	}
	descriptor, err := json.Marshal(p.Descriptor)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)
	if err := q.LockInstalledPackages(ctx, p.OrgID); err != nil {
		return err
	}
	if err := checkInstalledRequirements(ctx, q, p); err != nil {
		return err
	}
	if err := q.InsertInstalledPackage(ctx, postgresdb.InsertInstalledPackageParams{
		OrgID: p.OrgID, Name: p.Name, Reference: p.Reference, Digest: p.Digest,
		Descriptor: descriptor, Layer: p.Layer, UpdatePolicy: p.UpdatePolicy,
	}); err != nil {
		if pgCode(err) == "23505" {
			return storepkg.ErrInstalled
		}
		return err
	}
	if err := insertInstalledRequirements(ctx, q, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func checkInstalledRequirements(ctx context.Context, q *postgresdb.Queries, p storepkg.Installed) error {
	for _, required := range p.Descriptor.Requires {
		existing, err := q.GetInstalledPackage(ctx, postgresdb.GetInstalledPackageParams{OrgID: p.OrgID, Name: required.Name})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("required package %q: %w", required.Name, storepkg.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if existing.Digest != required.Digest {
			return fmt.Errorf("required package %q has a different digest", required.Name)
		}
	}
	return nil
}

func insertInstalledRequirements(ctx context.Context, q *postgresdb.Queries, p storepkg.Installed) error {
	for _, required := range p.Descriptor.Requires {
		if err := q.InsertInstalledRequirement(ctx, postgresdb.InsertInstalledRequirementParams{
			OrgID: p.OrgID, PackageName: p.Name, RequiredName: required.Name, RequiredDigest: required.Digest,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *InstalledPackages) Get(ctx context.Context, orgID, name string) (storepkg.Installed, error) {
	row, err := postgresdb.New(s.pool).GetInstalledPackage(ctx, postgresdb.GetInstalledPackageParams{OrgID: orgID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return storepkg.Installed{}, storepkg.ErrNotFound
	}
	if err != nil {
		return storepkg.Installed{}, err
	}
	return installedFromRow(row.OrgID, row.Name, row.Reference, row.Digest, row.Descriptor, row.Layer, row.UpdatePolicy)
}

func (s *InstalledPackages) List(ctx context.Context, orgID string) ([]storepkg.Installed, error) {
	rows, err := postgresdb.New(s.pool).ListInstalledPackages(ctx, orgID)
	if err != nil {
		return nil, err
	}
	packages := make([]storepkg.Installed, 0, len(rows))
	for _, row := range rows {
		p, err := installedFromRow(row.OrgID, row.Name, row.Reference, row.Digest, row.Descriptor, row.Layer, row.UpdatePolicy)
		if err != nil {
			return nil, err
		}
		packages = append(packages, p)
	}
	return packages, nil
}

func (s *InstalledPackages) Remove(ctx context.Context, orgID, name string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)
	if err := q.LockInstalledPackages(ctx, orgID); err != nil {
		return err
	}
	rows, err := q.DeleteInstalledPackage(ctx, postgresdb.DeleteInstalledPackageParams{OrgID: orgID, Name: name})
	if pgCode(err) == "23001" {
		return storepkg.ErrRequired
	}
	if err != nil {
		return err
	}
	if rows == 0 {
		return storepkg.ErrNotFound
	}
	return tx.Commit(ctx)
}

func installedFromRow(orgID, name, reference, digest string, raw, layer []byte, updatePolicy string) (storepkg.Installed, error) {
	var descriptor storepkg.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		return storepkg.Installed{}, fmt.Errorf("decode installed package: %w", err)
	}
	return storepkg.Installed{OrgID: orgID, Name: name, Reference: reference, Digest: digest, Descriptor: descriptor, Layer: layer, UpdatePolicy: updatePolicy}, nil
}

func pgCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}
