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
	b.pg = pool
	b.addCleanup(pool.Close)
	return nil
}
