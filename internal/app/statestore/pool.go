package statestore

import (
	"context"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
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
func (b *server) openStateStorePool(ctx context.Context) error {
	pool, err := servicekit.OpenPool(ctx, b.cfg.DatabaseURL, "the State Store")
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
