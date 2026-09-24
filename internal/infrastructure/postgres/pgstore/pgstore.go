// Package pgstore opens migrated PostgreSQL stores on a throwaway pgtest
// database, for tests outside internal/infrastructure/postgres. A package
// using it calls pgtest.Main from TestMain.
package pgstore

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/bindingcipher"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// TaskDB is the State Store's task database: the task, event, status and
// identity surface plus the control-plane resources, over one pool.
type TaskDB struct {
	*postgres.Store
	*postgres.Resources
	Pool *pgxpool.Pool
}

// Pool opens a pool on a fresh migrated database, closed when t ends.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return PoolAt(t, pgtest.URL(t))
}

// PoolAt opens a pool on url and migrates it, closed when t ends. Opening a
// second pool on the same url is how a test models a restart.
func PoolAt(t testing.TB, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.Open(t.Context(), url)
	if err != nil {
		t.Fatalf("pgstore: open: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		t.Fatalf("pgstore: migrate: %v", err)
	}
	return pool
}

// On returns the task database over pool.
func On(pool *pgxpool.Pool) *TaskDB {
	return &TaskDB{Store: postgres.New(pool), Resources: postgres.NewResources(pool), Pool: pool}
}

// Open returns the task database on a fresh migrated database.
func Open(t testing.TB) *TaskDB {
	t.Helper()
	return On(Pool(t))
}

// EDA returns the event-capture store on a fresh migrated database. A nil
// cipher keeps binding secrets in plaintext.
func EDA(t testing.TB, cipher bindingcipher.BindingCipher) *postgres.EDA {
	t.Helper()
	return postgres.NewEDA(Pool(t), cipher)
}
