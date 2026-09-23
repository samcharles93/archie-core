package postgres

import (
	"errors"
	"os"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func TestCheckServerVersion(t *testing.T) {
	tests := []struct {
		num  int
		want error
	}{
		{num: 180000},
		{num: 180006},
		{num: 190000},
		{num: 179999, want: ErrServerTooOld},
		{num: 170011, want: ErrServerTooOld},
	}
	for _, tt := range tests {
		if err := checkServerVersion(tt.num); !errors.Is(err, tt.want) {
			t.Errorf("checkServerVersion(%d) = %v, want %v", tt.num, err, tt.want)
		}
	}
}

func TestOpenConnectsToPostgres18(t *testing.T) {
	pool, err := Open(t.Context(), pgtest.URL(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	var one int
	if err := pool.QueryRow(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 = %d, %v", one, err)
	}
}

// Each migration inserts one row, so the row count is the number of times
// it was applied.
var countingMigrations = fstest.MapFS{
	"00001_create.sql": {Data: []byte("-- +goose Up\nCREATE TABLE applied (n int);\nINSERT INTO applied VALUES (1);\n")},
	"00002_again.sql":  {Data: []byte("-- +goose Up\nINSERT INTO applied VALUES (2);\n")},
}

func TestMigrateAppliesEachMigrationOnce(t *testing.T) {
	pool, err := Open(t.Context(), pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Two processes starting together, then a later restart.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { errs[i] = Migrate(t.Context(), pool, countingMigrations) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate %d: %v", i, err)
		}
	}
	if err := Migrate(t.Context(), pool, countingMigrations); err != nil {
		t.Fatalf("Migrate on restart: %v", err)
	}

	var rows int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM applied").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("applied rows = %d, want 2 (each migration exactly once)", rows)
	}
}
