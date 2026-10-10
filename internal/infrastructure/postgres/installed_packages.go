package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	return installedFromRow(row.OrgID, row.Name, row.Reference, row.Digest, row.Descriptor, row.Layer, row.UpdatePolicy, row.AcceptedAuthority, updateColumns{row.PendingReference, row.PendingDigest, row.PreviousReference, row.PreviousDigest, row.PendingAuthority, row.PreviousAcceptedAuthority})
}

func (s *InstalledPackages) List(ctx context.Context, orgID string) ([]storepkg.Installed, error) {
	rows, err := postgresdb.New(s.pool).ListInstalledPackages(ctx, orgID)
	if err != nil {
		return nil, err
	}
	packages := make([]storepkg.Installed, 0, len(rows))
	for _, row := range rows {
		p, err := installedFromRow(row.OrgID, row.Name, row.Reference, row.Digest, row.Descriptor, row.Layer, row.UpdatePolicy, row.AcceptedAuthority, updateColumns{row.PendingReference, row.PendingDigest, row.PreviousReference, row.PreviousDigest, row.PendingAuthority, row.PreviousAcceptedAuthority})
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

func (s *InstalledPackages) Accept(ctx context.Context, orgID, name string, authority storepkg.Authority) error {
	if orgID == "" || name == "" {
		return errors.New("org and name are required")
	}
	if err := authority.Validate(); err != nil {
		return fmt.Errorf("accepted authority: %w", err)
	}
	encoded, err := json.Marshal(authority)
	if err != nil {
		return err
	}
	rows, err := postgresdb.New(s.pool).AcceptInstalledPackageAuthority(ctx, postgresdb.AcceptInstalledPackageAuthorityParams{
		OrgID: orgID, Name: name, AcceptedAuthority: encoded,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return storepkg.ErrNotFound
	}
	return nil
}

// updateColumns are the nullable pending and previous pin columns of a row.
type updateColumns struct {
	pendingReference, pendingDigest, previousReference, previousDigest pgtype.Text
	pendingAuthority, previousAccepted                                 []byte
}

func pinOf(reference, digest pgtype.Text) *storepkg.Pin {
	if !reference.Valid || !digest.Valid {
		return nil
	}
	return &storepkg.Pin{Reference: reference.String, Digest: digest.String}
}

func (s *InstalledPackages) SetPending(ctx context.Context, orgID, name string, pending storepkg.Pin, declared storepkg.Authority) error {
	encoded, err := json.Marshal(declared)
	if err != nil {
		return err
	}
	rows, err := postgresdb.New(s.pool).SetInstalledPackagePending(ctx, postgresdb.SetInstalledPackagePendingParams{
		OrgID: orgID, Name: name, PendingReference: pgtype.Text{String: pending.Reference, Valid: true}, PendingDigest: pgtype.Text{String: pending.Digest, Valid: true}, PendingAuthority: encoded,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return storepkg.ErrNotFound
	}
	return nil
}

// Replace swaps the live pin under the org's package lock. Another package
// pinning this one's digest, or a requirement the new descriptor cannot
// meet, refuses the swap.
func (s *InstalledPackages) Replace(ctx context.Context, p storepkg.Installed) error {
	if err := p.Descriptor.Validate(); err != nil {
		return err
	}
	descriptor, err := json.Marshal(p.Descriptor)
	if err != nil {
		return err
	}
	var accepted []byte
	if p.AcceptedAuthority != nil {
		if accepted, err = json.Marshal(p.AcceptedAuthority); err != nil {
			return err
		}
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
	dependents, err := q.CountInstalledDependents(ctx, postgresdb.CountInstalledDependentsParams{OrgID: p.OrgID, RequiredName: p.Name})
	if err != nil {
		return err
	}
	if dependents > 0 {
		return storepkg.ErrRequired
	}
	if err := checkInstalledRequirements(ctx, q, p); err != nil {
		return err
	}
	rows, err := q.ReplaceInstalledPackage(ctx, postgresdb.ReplaceInstalledPackageParams{
		OrgID: p.OrgID, Name: p.Name, Reference: p.Reference, Digest: p.Digest,
		Descriptor: descriptor, Layer: p.Layer, AcceptedAuthority: accepted,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return storepkg.ErrNotFound
	}
	if err := q.DeleteInstalledRequirements(ctx, postgresdb.DeleteInstalledRequirementsParams{OrgID: p.OrgID, PackageName: p.Name}); err != nil {
		return err
	}
	if err := insertInstalledRequirements(ctx, q, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func installedFromRow(orgID, name, reference, digest string, raw, layer []byte, updatePolicy string, accepted []byte, update updateColumns) (storepkg.Installed, error) {
	var descriptor storepkg.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		return storepkg.Installed{}, fmt.Errorf("decode installed package: %w", err)
	}
	authority, err := acceptedFromRow(accepted)
	if err != nil {
		return storepkg.Installed{}, err
	}
	pendingAuthority, err := acceptedFromRow(update.pendingAuthority)
	if err != nil {
		return storepkg.Installed{}, err
	}
	previousAccepted, err := acceptedFromRow(update.previousAccepted)
	if err != nil {
		return storepkg.Installed{}, err
	}
	return storepkg.Installed{
		OrgID: orgID, Name: name, Reference: reference, Digest: digest, Descriptor: descriptor, Layer: layer,
		UpdatePolicy: updatePolicy, AcceptedAuthority: authority,
		Pending: pinOf(update.pendingReference, update.pendingDigest), PendingAuthority: pendingAuthority,
		Previous: pinOf(update.previousReference, update.previousDigest), PreviousAccepted: previousAccepted,
	}, nil
}

func acceptedFromRow(raw []byte) (*storepkg.Authority, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var authority storepkg.Authority
	if err := json.Unmarshal(raw, &authority); err != nil {
		return nil, fmt.Errorf("decode accepted authority: %w", err)
	}
	return &authority, nil
}

func pgCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

func (s *InstalledPackages) SetUpdatePolicy(ctx context.Context, orgID, name, policy string) error {
	rows, err := postgresdb.New(s.pool).SetInstalledPackageUpdatePolicy(ctx, postgresdb.SetInstalledPackageUpdatePolicyParams{
		OrgID: orgID, Name: name, UpdatePolicy: policy,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return storepkg.ErrNotFound
	}
	return nil
}

func (s *InstalledPackages) ListAutoUpdate(ctx context.Context) ([]storepkg.OrgPackage, error) {
	rows, err := postgresdb.New(s.pool).ListAutoUpdateInstalledPackages(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]storepkg.OrgPackage, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, storepkg.OrgPackage{OrgID: row.OrgID, Name: row.Name})
	}
	return refs, nil
}
