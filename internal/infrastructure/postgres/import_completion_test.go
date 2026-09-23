package postgres

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestImportComplete(t *testing.T) {
	tests := []struct {
		name   string
		record bool
		want   bool
	}{
		{name: "migrated database without a record", want: false},
		{name: "committed record", record: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := openPool(t, pgtest.URL(t))
			if err := Migrate(t.Context(), pool, Migrations()); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if tt.record {
				if _, err := pool.Exec(t.Context(),
					"INSERT INTO import_completion (id, sources, report) VALUES (1, 's', 'r')"); err != nil {
					t.Fatalf("seed record: %v", err)
				}
			}
			got, err := ImportComplete(t.Context(), pool)
			if err != nil {
				t.Fatalf("ImportComplete: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ImportComplete = %v, want %v", got, tt.want)
			}
		})
	}
}
