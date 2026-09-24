package archied

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// openStateStorePool opens the State Store process's one process-scoped
// PostgreSQL pool, applies the schema and claims serve ownership of the
// database for the process's life, so a second State Store against the same
// database refuses to start. A missing or invalid database_url or a migration
// failure is a startup failure.
//
// The pool is stored on boot.pg and closed at shutdown; nothing opens a second
// pool per subsystem.
func (b *boot) openStateStorePool(ctx context.Context) error {
	pool, err := openServicePool(ctx, b.cfg.DatabaseURL, "the State Store")
	if err != nil {
		b.log.Error("open state store postgres pool", "err", err)
		return err
	}
	ownership, err := postgres.AcquireOwnership(ctx, pool, postgres.OwnerStateStore)
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

// openServicePool opens and migrates a serving process's pool. service names
// the process in the error an operator reads.
func openServicePool(ctx context.Context, url, service string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, fmt.Errorf("database_url is required: %s is Postgres-only", service)
	}
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
