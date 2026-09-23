package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// Service owner names: every Archie process that serves this database holds
// one of these claims for its whole life, so an offline rewrite checks them
// all.
const (
	OwnerStateStore = "state-store"
	OwnerGateway    = "gateway"
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
