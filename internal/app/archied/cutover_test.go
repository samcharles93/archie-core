package archied

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/legacyimport/legacyfixture"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// seedLegacyConversation writes one session into the legacy SQLite
// conversation file beside dbPath, which is enough for the cutover gate to
// see legacy data.
func seedLegacyConversation(t *testing.T, dbPath string) {
	t.Helper()
	legacyfixture.Exec(t, conversationDBPath(dbPath), legacyfixture.GatewayDDL)
}

func testPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.Open(t.Context(), url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// TestCutoverGate pins D1.3 in both serving processes: a fresh install boots
// and records completion, legacy data without a completed import refuses with
// the operator's instruction, and a completed import boots over legacy files.
func TestCutoverGate(t *testing.T) {
	processes := []struct {
		name string
		open func(*boot, context.Context) error
	}{
		{"state store", func(b *boot, ctx context.Context) error { return b.openStateStorePool(ctx) }},
		{"gateway", func(b *boot, ctx context.Context) error { return b.openStores(ctx) }},
	}
	tests := []struct {
		name     string
		legacy   bool
		complete bool
		wantErr  string
	}{
		{name: "fresh install"},
		{name: "legacy data without import", legacy: true, wantErr: "run `archie-state-store import`"},
		{name: "legacy data with completed import", legacy: true, complete: true},
	}
	for _, p := range processes {
		for _, tt := range tests {
			t.Run(p.name+"/"+tt.name, func(t *testing.T) {
				url := pgtest.URL(t)
				dbPath := filepath.Join(t.TempDir(), "archie.db")
				if tt.legacy {
					seedLegacyConversation(t, dbPath)
				}
				pool := testPool(t, url)
				if tt.complete {
					if _, err := pool.Exec(t.Context(),
						"INSERT INTO import_completion (id, sources, report) VALUES (1, 's', 'r')"); err != nil {
						t.Fatalf("seed completion: %v", err)
					}
				}
				b := newBootstrap()
				b.cfg = config.Config{DatabaseURL: url, DBPath: dbPath}
				err := p.open(b, t.Context())
				b.cleanup()
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("open = %v, want refusal containing %q", err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				done, err := postgres.ImportComplete(t.Context(), pool)
				if err != nil {
					t.Fatalf("ImportComplete: %v", err)
				}
				if !done {
					t.Fatal("serving database has no completion record; a later legacy file would block it")
				}
			})
		}
	}
}

// TestFreshInstallIgnoresLaterLegacyFile pins the fresh-install record: once a
// database started with no legacy data, a stray legacy file does not block it.
func TestFreshInstallIgnoresLaterLegacyFile(t *testing.T) {
	url := pgtest.URL(t)
	dbPath := filepath.Join(t.TempDir(), "archie.db")
	first := newBootstrap()
	first.cfg = config.Config{DatabaseURL: url, DBPath: dbPath}
	if err := first.openStateStorePool(t.Context()); err != nil {
		t.Fatalf("fresh start: %v", err)
	}
	first.cleanup()
	seedLegacyConversation(t, dbPath)
	if _, err := os.Stat(conversationDBPath(dbPath)); err != nil {
		t.Fatalf("legacy file not written: %v", err)
	}
	second := newBootstrap()
	second.cfg = first.cfg
	if err := second.openStateStorePool(t.Context()); err != nil {
		t.Fatalf("restart with a stray legacy file: %v", err)
	}
	second.cleanup()
}

// TestGatewayRefusesASecondOwner pins D4.1: one Gateway serves a database, a
// second refuses while the first holds its claim, and one starts again once
// the first has shut down.
func TestGatewayRefusesASecondOwner(t *testing.T) {
	cfg := config.Config{DatabaseURL: pgtest.URL(t), DBPath: filepath.Join(t.TempDir(), "archie.db")}
	start := func() (*boot, error) {
		b := newBootstrap()
		b.cfg = cfg
		if err := b.openChatSessions(t.Context()); err != nil {
			b.cleanup()
			return nil, err
		}
		if err := b.claimGatewayOwnership(t.Context()); err != nil {
			b.cleanup()
			return nil, err
		}
		return b, nil
	}
	first, err := start()
	if err != nil {
		t.Fatalf("first gateway: %v", err)
	}
	if _, err := start(); !errors.Is(err, postgres.ErrOwned) {
		first.cleanup()
		t.Fatalf("second gateway = %v, want ErrOwned", err)
	}
	first.cleanup()
	third, err := start()
	if err != nil {
		t.Fatalf("gateway after the owner shut down: %v", err)
	}
	third.cleanup()
}

// TestConversationStoreFailsClosedWithoutURL: the conversation store is
// Postgres-only, so a missing database_url stops the process.
func TestConversationStoreFailsClosedWithoutURL(t *testing.T) {
	b := newBootstrap()
	defer b.cleanup()
	err := b.openChatSessions(t.Context())
	if err == nil || !strings.Contains(err.Error(), "database_url is required") {
		t.Fatalf("openChatSessions = %v, want it to name the missing database_url", err)
	}
}
