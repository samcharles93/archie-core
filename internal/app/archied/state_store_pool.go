package archied

import (
	"context"
	"errors"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// openStateStorePool opens the State Store process's one process-scoped
// PostgreSQL pool and applies the schema. It is the fail-closed seam for the
// Postgres-only epic: a missing or invalid database_url stops the State Store
// from starting rather than falling back to SQLite, and a migration failure is
// a startup failure, not a warning.
//
// It then claims serve ownership of the database for the process's life, so a
// second State Store against the same database refuses to start.
//
// The pool is stored on boot.pg and closed at shutdown; nothing opens a second
// pool per subsystem.
func (b *boot) openStateStorePool(ctx context.Context) error {
	url := b.cfg.DatabaseURL
	if url == "" {
		err := errors.New("database_url is required: the State Store is Postgres-only and refuses to fall back to SQLite")
		b.log.Error("open state store postgres pool", "err", err)
		return err
	}
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		b.log.Error("open state store postgres pool", "err", err)
		return err
	}
	if err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		pool.Close()
		b.log.Error("migrate state store postgres schema", "err", err)
		return err
	}
	ownership, err := postgres.AcquireOwnership(ctx, pool, "state-store")
	if err != nil {
		pool.Close()
		b.log.Error("claim state store ownership", "err", err)
		return err
	}
	b.pg = pool
	b.addCleanup(pool.Close)
	// Registered after the pool so it runs first: the claim is dropped before
	// the pool closes.
	releaseCtx := context.WithoutCancel(ctx)
	b.addCleanup(func() { _ = ownership.Release(releaseCtx) })
	return nil
}
