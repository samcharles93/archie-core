package servicekit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// OpenPool opens and migrates the PostgreSQL pool a service owns its tables in.
// service names the owner in the error an empty database_url produces.
func OpenPool(ctx context.Context, url, service string) (*pgxpool.Pool, error) {
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
