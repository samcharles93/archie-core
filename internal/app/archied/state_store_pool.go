package archied

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyimport"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// openStateStorePool opens the State Store process's one process-scoped
// PostgreSQL pool, applies the schema and claims serve ownership of the
// database for the process's life, so a second State Store against the same
// database refuses to start. A missing or invalid database_url, a migration
// failure or unimported legacy data is a startup failure.
//
// The pool is stored on boot.pg and closed at shutdown; nothing opens a second
// pool per subsystem.
func (b *boot) openStateStorePool(ctx context.Context) error {
	pool, err := openServicePool(ctx, b.cfg.DatabaseURL, b.cfg.DBPath, "the State Store")
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

// openServicePool opens and migrates a serving process's pool and refuses to
// serve over legacy data the one-time import has not moved. service names the
// process in the error an operator reads.
func openServicePool(ctx context.Context, url, dbPath, service string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, fmt.Errorf("database_url is required: %s is Postgres-only and refuses to fall back to SQLite", service)
	}
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		pool.Close()
		return nil, err
	}
	if err := refuseUnimportedLegacy(ctx, pool, dbPath); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// refuseUnimportedLegacy is the cutover gate. A database with a completion
// record serves. Otherwise non-empty legacy SQLite files mean an upgrade whose
// import has not run, and serving would hide that data behind empty tables,
// so it refuses. With no legacy data it is a fresh install: the completion
// record is written so a legacy file that appears later cannot block it.
func refuseUnimportedLegacy(ctx context.Context, pool *pgxpool.Pool, dbPath string) error {
	done, err := postgres.ImportComplete(ctx, pool)
	if err != nil || done {
		return err
	}
	sources := legacySources(dbPath)
	has, err := legacyimport.HasLegacyData(ctx, sources)
	if err != nil {
		return fmt.Errorf("check for legacy SQLite data: %w", err)
	}
	if has {
		return fmt.Errorf("legacy SQLite data found (%s) but this Postgres database has no completed import: "+
			"stop every archie process, run `archie-state-store import`, then start archie again",
			strings.Join([]string{sources.StateStore, sources.EDA, sources.Gateway}, ", "))
	}
	return postgres.RecordFreshInstall(ctx, pool)
}
