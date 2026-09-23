// Package postgres opens archie's PostgreSQL database and applies its schema
// migrations.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// minServerVersion is PostgreSQL 18 in server_version_num form.
const minServerVersion = 180000

// ErrServerTooOld is returned for a server older than PostgreSQL 18.
var ErrServerTooOld = errors.New("postgres: server is older than PostgreSQL 18")

func checkServerVersion(num int) error {
	if num < minServerVersion {
		return fmt.Errorf("%w (server_version_num %d)", ErrServerTooOld, num)
	}
	return nil
}

// Open connects a pool to url and refuses a server older than PostgreSQL 18.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	var num int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&num); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: read server version: %w", err)
	}
	if err := checkServerVersion(num); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate applies the goose migrations in fsys. A session-level advisory lock
// makes processes that start together apply each migration once.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("postgres: migration lock: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("postgres: migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}
