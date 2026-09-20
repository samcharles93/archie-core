package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

const identitiesSchema = `
CREATE TABLE IF NOT EXISTS identities (
 id TEXT PRIMARY KEY NOT NULL, kind TEXT NOT NULL, display_name TEXT NOT NULL,
 lifecycle TEXT NOT NULL, version INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS identity_aliases (
 alias TEXT PRIMARY KEY COLLATE NOCASE NOT NULL, identity_id TEXT NOT NULL REFERENCES identities(id)
);
CREATE TABLE IF NOT EXISTS identity_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, identity_id TEXT NOT NULL, event_type TEXT NOT NULL,
 from_lifecycle TEXT NOT NULL, to_lifecycle TEXT NOT NULL, display_name TEXT NOT NULL,
 actor_id TEXT NOT NULL, source TEXT NOT NULL, request_id TEXT NOT NULL UNIQUE, at TEXT NOT NULL
);`

const identityTimeLayout = time.RFC3339Nano

func (s *Store) List(ctx context.Context) (_ []identity.Identity, retErr error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, display_name, lifecycle, version, created_at, updated_at FROM identities ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	var out []identity.Identity
	for rows.Next() {
		value, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id identity.IdentityID) (identity.Identity, error) {
	value, err := scanIdentity(s.db.QueryRowContext(ctx, `SELECT id, kind, display_name, lifecycle, version, created_at, updated_at FROM identities WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	return value, err
}

func (s *Store) ResolveLegacyName(ctx context.Context, name string) (identity.Identity, error) {
	value, err := scanIdentity(s.db.QueryRowContext(ctx, `SELECT i.id, i.kind, i.display_name, i.lifecycle, i.version, i.created_at, i.updated_at FROM identities i JOIN identity_aliases a ON a.identity_id=i.id WHERE a.alias=?`, strings.TrimSpace(name)))
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	return value, err
}

func (s *Store) Create(ctx context.Context, value identity.Identity, audit identity.Audit) (identity.Identity, error) {
	if err := value.Validate(); err != nil {
		return identity.Identity{}, err
	}
	now := audit.At.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	value.Version, value.CreatedAt, value.UpdatedAt = 1, now, now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Identity{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO identities(id,kind,display_name,lifecycle,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, value.ID, value.Kind, value.DisplayName, value.Lifecycle, value.Version, now.Format(identityTimeLayout), now.Format(identityTimeLayout)); err != nil {
		return identity.Identity{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO identity_aliases(alias,identity_id) VALUES(?,?)`, value.DisplayName, value.ID); err != nil {
		return identity.Identity{}, err
	}
	if err = insertIdentityEvent(ctx, tx, value, identity.Event{IdentityID: value.ID, Type: "create", To: value.Lifecycle, DisplayName: value.DisplayName}, audit, now); err != nil {
		return identity.Identity{}, err
	}
	return value, tx.Commit()
}

func (s *Store) Apply(ctx context.Context, id identity.IdentityID, expectedVersion int64, command identity.Command, audit identity.Audit) (identity.Identity, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Identity{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanIdentity(tx.QueryRowContext(ctx, `SELECT id, kind, display_name, lifecycle, version, created_at, updated_at FROM identities WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Identity{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Identity{}, err
	}
	if current.Version != expectedVersion {
		return identity.Identity{}, identity.ErrConflict
	}
	next, event, err := identity.Apply(current, command)
	if err != nil {
		return identity.Identity{}, err
	}
	now := audit.At.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	next.UpdatedAt = now
	result, err := tx.ExecContext(ctx, `UPDATE identities SET display_name=?, lifecycle=?, version=?, updated_at=? WHERE id=? AND version=?`, next.DisplayName, next.Lifecycle, next.Version, now.Format(identityTimeLayout), id, expectedVersion)
	if err != nil {
		return identity.Identity{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return identity.Identity{}, identity.ErrConflict
	}
	if command.Type == identity.CommandRename {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO identity_aliases(alias,identity_id) VALUES(?,?)`, next.DisplayName, id); err != nil {
			return identity.Identity{}, err
		}
	}
	if err = insertIdentityEvent(ctx, tx, next, event, audit, now); err != nil {
		return identity.Identity{}, err
	}
	return next, tx.Commit()
}

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
		_, err = s.Create(ctx, value, identity.Audit{ActorID: identity.SystemID, Source: "legacy-config", RequestID: "identity-import:" + string(seed.id)})
		if err != nil && !isSQLiteUnique(err) {
			return fmt.Errorf("bootstrap identity %q: %w", seed.name, err)
		}
		if _, aliasErr := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO identity_aliases(alias,identity_id) VALUES(?,?)`, seed.name, seed.id); aliasErr != nil {
			return aliasErr
		}
		if seed.kind != identity.KindSystem {
			if _, migrateErr := s.db.ExecContext(ctx, `UPDATE tasks SET identity=? WHERE identity=?`, seed.id, seed.name); migrateErr != nil {
				return migrateErr
			}
		}
	}
	return nil
}

type identityScanner interface{ Scan(...any) error }

func scanIdentity(row identityScanner) (identity.Identity, error) {
	var v identity.Identity
	var created, updated string
	err := row.Scan(&v.ID, &v.Kind, &v.DisplayName, &v.Lifecycle, &v.Version, &created, &updated)
	if err != nil {
		return v, err
	}
	v.CreatedAt, err = time.Parse(identityTimeLayout, created)
	if err != nil {
		return v, err
	}
	v.UpdatedAt, err = time.Parse(identityTimeLayout, updated)
	return v, err
}

func insertIdentityEvent(ctx context.Context, tx *sql.Tx, value identity.Identity, event identity.Event, audit identity.Audit, at time.Time) error {
	if audit.ActorID == "" || audit.Source == "" || audit.RequestID == "" {
		return fmt.Errorf("%w: audit actor, source, and request ID are required", identity.ErrInvalid)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_events(identity_id,event_type,from_lifecycle,to_lifecycle,display_name,actor_id,source,request_id,at) VALUES(?,?,?,?,?,?,?,?,?)`, value.ID, event.Type, event.From, event.To, event.DisplayName, audit.ActorID, audit.Source, audit.RequestID, at.Format(identityTimeLayout))
	return err
}

func isSQLiteUnique(err error) bool { return strings.Contains(err.Error(), "UNIQUE constraint failed") }
