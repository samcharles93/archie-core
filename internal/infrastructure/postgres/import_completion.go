package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// ImportComplete reports whether the one-time legacy import committed and
// verified against this migrated database. Serving over non-empty legacy
// files is refused without it.
func ImportComplete(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	done, err := postgresdb.New(pool).ImportCompleted(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: import completion: %w", err)
	}
	return done, nil
}

// RecordFreshInstall writes the completion record for a database that starts
// with no legacy data to import, so a legacy file that appears later cannot
// block it from serving. It leaves an existing record untouched.
func RecordFreshInstall(ctx context.Context, pool *pgxpool.Pool) error {
	if err := postgresdb.New(pool).InsertFreshInstallCompletion(ctx); err != nil {
		return fmt.Errorf("postgres: fresh install completion: %w", err)
	}
	return nil
}
