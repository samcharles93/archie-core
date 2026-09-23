package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// identityFromRow maps the generated identity row to the domain identity.
func identityFromRow(i postgresdb.Identity) identity.Identity {
	return identity.Identity{
		ID: identity.IdentityID(i.ID), Kind: identity.Kind(i.Kind),
		DisplayName: i.DisplayName, Lifecycle: identity.Lifecycle(i.Lifecycle),
		Version: i.Version, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

// insertIdentityEvent writes one identity audit event through q, refusing an
// audit that cannot answer who acted and whose authority they used.
func insertIdentityEvent(ctx context.Context, q *postgresdb.Queries, id identity.IdentityID, ev identity.Event, audit identity.Audit, at time.Time) error {
	if audit.ActorID == "" || audit.Source == "" || audit.RequestID == "" {
		return fmt.Errorf("%w: audit actor, source, and request ID are required", identity.ErrInvalid)
	}
	return q.InsertIdentityEvent(ctx, postgresdb.InsertIdentityEventParams{
		IdentityID: string(id), EventType: string(ev.Type),
		FromLifecycle: string(ev.From), ToLifecycle: string(ev.To),
		DisplayName: ev.DisplayName, ActorID: string(audit.ActorID),
		Source: audit.Source, RequestID: audit.RequestID, At: at,
	})
}

// List returns every identity, oldest first.
func (s *Store) List(ctx context.Context) ([]identity.Identity, error) {
	rows, err := s.queries().ListIdentities(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]identity.Identity, 0, len(rows))
	for _, r := range rows {
		out = append(out, identityFromRow(r))
	}
	return out, nil
}

// Get returns one identity by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id identity.IdentityID) (identity.Identity, error) {
	i, err := s.queries().GetIdentity(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Identity{}, err
	}
	return identityFromRow(i), nil
}

// ResolveLegacyName resolves a legacy display name through the aliases table.
// Aliases match case-insensitively, mirroring the SQLite schema's COLLATE
// NOCASE.
func (s *Store) ResolveLegacyName(ctx context.Context, name string) (identity.Identity, error) {
	i, err := s.queries().ResolveIdentityAlias(ctx, strings.TrimSpace(name))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Identity{}, err
	}
	return identityFromRow(i), nil
}

// Create inserts a new identity, its display-name alias and its create audit
// event in one transaction.
func (s *Store) Create(ctx context.Context, value identity.Identity, audit identity.Audit) (identity.Identity, error) {
	if err := value.Validate(); err != nil {
		return identity.Identity{}, err
	}
	now := audit.At.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	value.Version, value.CreatedAt, value.UpdatedAt = 1, now, now

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.Identity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := q.InsertIdentity(ctx, postgresdb.InsertIdentityParams{
		ID: string(value.ID), Kind: string(value.Kind), DisplayName: value.DisplayName,
		Lifecycle: string(value.Lifecycle), Version: value.Version, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return identity.Identity{}, err
	}
	if err := q.InsertIdentityAlias(ctx, postgresdb.InsertIdentityAliasParams{
		Alias: value.DisplayName, IdentityID: string(value.ID),
	}); err != nil {
		return identity.Identity{}, err
	}
	if err := insertIdentityEvent(ctx, q, value.ID, identity.Event{
		IdentityID: value.ID, Type: "create", To: value.Lifecycle, DisplayName: value.DisplayName,
	}, audit, now); err != nil {
		return identity.Identity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.Identity{}, err
	}
	return value, nil
}

// Apply mutates an identity under an optimistic version guard and records the
// lifecycle event.
func (s *Store) Apply(ctx context.Context, id identity.IdentityID, expectedVersion int64, command identity.Command, audit identity.Audit) (identity.Identity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.Identity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	current, err := q.GetIdentity(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Identity{}, err
	}
	cur := identityFromRow(current)
	if cur.Version != expectedVersion {
		return identity.Identity{}, identity.ErrConflict
	}
	next, ev, err := identity.Apply(cur, command)
	if err != nil {
		return identity.Identity{}, err
	}
	now := audit.At.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	next.UpdatedAt = now

	n, err := q.UpdateIdentity(ctx, postgresdb.UpdateIdentityParams{
		ID: string(id), DisplayName: next.DisplayName, Lifecycle: string(next.Lifecycle),
		Version: next.Version, UpdatedAt: now, Version_2: expectedVersion,
	})
	if err != nil {
		return identity.Identity{}, err
	}
	if n != 1 {
		return identity.Identity{}, identity.ErrConflict
	}
	if command.Type == identity.CommandRename {
		if err := q.InsertIdentityAliasIgnoreConflict(ctx, postgresdb.InsertIdentityAliasIgnoreConflictParams{
			Alias: next.DisplayName, IdentityID: string(id),
		}); err != nil {
			return identity.Identity{}, err
		}
	}
	if err := insertIdentityEvent(ctx, q, id, ev, audit, now); err != nil {
		return identity.Identity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.Identity{}, err
	}
	return next, nil
}

// ResolveSubject returns the identity bound to a provider subject.
func (s *Store) ResolveSubject(ctx context.Context, subject identity.Subject) (identity.Identity, error) {
	i, err := s.queries().ResolveIdentitySubject(ctx, postgresdb.ResolveIdentitySubjectParams{
		Issuer: subject.Issuer, Subject: subject.Subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Identity{}, err
	}
	return identityFromRow(i), nil
}

// BindSubject binds an identity to the provider subject that asserts it,
// moving any prior binding for that subject.
func (s *Store) BindSubject(ctx context.Context, id identity.IdentityID, subject identity.Subject, audit identity.Audit) error {
	if err := subject.Validate(); err != nil {
		return err
	}
	if audit.ActorID == "" || audit.Source == "" || audit.RequestID == "" {
		return fmt.Errorf("%w: audit actor, source, and request ID are required", identity.ErrInvalid)
	}
	now := audit.At.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	current, err := q.GetIdentity(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	cur := identityFromRow(current)

	if err := q.UpsertIdentitySubject(ctx, postgresdb.UpsertIdentitySubjectParams{
		Issuer: subject.Issuer, Subject: subject.Subject, IdentityID: string(id), BoundAt: now,
	}); err != nil {
		return err
	}
	if err := insertIdentityEvent(ctx, q, id, identity.Event{
		IdentityID: id, Type: "bind_subject", From: cur.Lifecycle, To: cur.Lifecycle, DisplayName: subject.Subject,
	}, audit, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BootstrapIdentities seeds the system identity and the configured legacy
// names idempotently, migrating any forge tasks that still carry a legacy
// name to the stable id.
func (s *Store) BootstrapIdentities(ctx context.Context, legacyNames []string) error {
	values := append([]struct {
		id   identity.IdentityID
		kind identity.Kind
		name string
	}{{identity.SystemID, identity.KindSystem, "System"}}, make([]struct {
		id   identity.IdentityID
		kind identity.Kind
		name string
	}, len(legacyNames))...)
	for i, name := range legacyNames {
		name = strings.TrimSpace(name)
		values[i+1] = struct {
			id   identity.IdentityID
			kind identity.Kind
			name string
		}{identity.StableID(name), identity.KindBot, name}
	}
	for _, seed := range values {
		if seed.name == "" {
			continue
		}
		value, err := identity.New(seed.id, seed.kind, seed.name)
		if err != nil {
			return err
		}
		_, err = s.Create(ctx, value, identity.Audit{
			ActorID: identity.SystemID, Source: "legacy-config", RequestID: "identity-import:" + string(seed.id),
		})
		if err != nil && !isUniqueViolation(err) {
			return fmt.Errorf("bootstrap identity %q: %w", seed.name, err)
		}
		if err := s.queries().InsertIdentityAliasIgnoreConflict(ctx, postgresdb.InsertIdentityAliasIgnoreConflictParams{
			Alias: seed.name, IdentityID: string(seed.id),
		}); err != nil {
			return err
		}
		if seed.kind != identity.KindSystem {
			if err := s.queries().UpdateTaskIdentity(ctx, postgresdb.UpdateTaskIdentityParams{
				Identity: string(seed.id), Identity_2: seed.name,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
