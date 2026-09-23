package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func openPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := Open(t.Context(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// claimOther takes name as another process would, then optionally releases it
// or kills its backend.
func claimOther(t *testing.T, url, name string, release, terminate bool) {
	t.Helper()
	other, err := AcquireOwnership(t.Context(), openPool(t, url), name)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	t.Cleanup(func() { _ = other.Release(t.Context()) })
	if release {
		if err := other.Release(t.Context()); err != nil {
			t.Fatalf("Release: %v", err)
		}
	}
	if terminate {
		if _, err := openPool(t, url).Exec(t.Context(), "SELECT pg_terminate_backend($1)", other.pid); err != nil {
			t.Fatalf("terminate: %v", err)
		}
	}
}

func TestAcquireOwnership(t *testing.T) {
	tests := []struct {
		name string
		// held is taken by another process before the claim under test.
		held string
		// release drops the other claim before the claim under test.
		release bool
		// terminate kills the other claim's backend, as a crash would.
		terminate bool
		want      error
	}{
		{name: "state-store", want: nil},
		{name: "state-store", held: "state-store", want: ErrOwned},
		{name: "state-store", held: "state-store", release: true, want: nil},
		{name: "state-store", held: "state-store", terminate: true, want: nil},
		{name: "gateway", held: "state-store", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/held="+tt.held, func(t *testing.T) {
			url := pgtest.URL(t)
			if tt.held != "" {
				claimOther(t, url, tt.held, tt.release, tt.terminate)
			}
			got, err := AcquireOwnership(t.Context(), openPool(t, url), tt.name)
			if got != nil {
				t.Cleanup(func() { _ = got.Release(t.Context()) })
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("AcquireOwnership(%q) = %v, want %v", tt.name, err, tt.want)
			}
		})
	}
}
