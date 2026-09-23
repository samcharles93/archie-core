package archied

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// pgRecoveryFixture is a migrated database and the config file naming it,
// which is all the Postgres recovery path is given: it resolves database_url
// from the configuration exactly as the State Store does.
func pgRecoveryFixture(t *testing.T) (configPath string, pool *pgxpool.Pool, url string) {
	t.Helper()
	url = pgtest.URL(t)
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.toml")
	body := fmt.Sprintf("bot_user = 'archie'\ndb_path = %q\ndatabase_url = %q\n[forge]\ntype = 'github'\nhost = 'https://github.example.com'\n",
		filepath.Join(dir, "archie.db"), url)
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		t.Fatal(err)
	}
	return configPath, pool, url
}

func requirePGTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH; this test needs the PostgreSQL 18 client tools", tool)
		}
	}
}

func putResource(t *testing.T, pool *pgxpool.Pool, kind, value, label string) {
	t.Helper()
	resources := postgres.NewResources(pool)
	expected := int64(0)
	if current, err := resources.Resource(t.Context(), kind); err == nil {
		expected = current.Version
	}
	if _, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: kind, Value: []byte(value), Actor: label, Source: "test",
		RequestID: fmt.Sprintf("%s-%d", label, expected+1), ExpectedVersion: expected,
	}); err != nil {
		t.Fatalf("put %s: %v", kind, err)
	}
}

func holdRole(t *testing.T, url, role string) {
	t.Helper()
	pool, err := postgres.Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	claim, err := postgres.AcquireOwnership(t.Context(), pool, role)
	if err != nil {
		t.Fatalf("hold %s: %v", role, err)
	}
	t.Cleanup(func() { _ = claim.Release(t.Context()) })
}

func TestPostgresRecoveryRequiresADatabaseURL(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("bot_user = 'archie'\n[forge]\ntype = 'github'\nhost = 'https://github.example.com'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{RecoveryBackup, RecoveryRestore, RecoveryValidate, RecoveryRollback} {
		t.Run(op, func(t *testing.T) {
			_, err := RunStateStoreRecovery(t.Context(), StateStoreRecoveryOptions{
				Operation: op, Config: configPath, Out: filepath.Join(dir, "o"), From: filepath.Join(dir, "f"), Kind: "k",
			})
			if err == nil || !strings.Contains(err.Error(), "database_url") {
				t.Fatalf("%s without database_url = %v, want a refusal naming database_url", op, err)
			}
		})
	}
}

func TestPostgresRecoveryBackupAndRestore(t *testing.T) {
	requirePGTools(t)
	configPath, pool, url := pgRecoveryFixture(t)
	putResource(t, pool, controlplane.ModelRoleAssignmentsKind, `{"implement":"anthropic/claude"}`, "snapshot")
	snapshot := filepath.Join(t.TempDir(), "snapshot.dump")

	if _, err := RunStateStoreRecovery(t.Context(), StateStoreRecoveryOptions{Operation: RecoveryBackup, Config: configPath, Out: snapshot}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	putResource(t, pool, controlplane.ModelRoleAssignmentsKind, `{"implement":"openai/gpt"}`, "later")

	for _, role := range postgres.ServiceOwners {
		t.Run("refused while "+role+" serves", func(t *testing.T) {
			holdRole(t, url, role)
			_, err := RunStateStoreRecovery(t.Context(), StateStoreRecoveryOptions{Operation: RecoveryRestore, Config: configPath, From: snapshot})
			if !errors.Is(err, postgres.ErrOwned) {
				t.Fatalf("restore with %s serving = %v, want ErrOwned", role, err)
			}
		})
	}

	summary, err := RunStateStoreRecovery(t.Context(), StateStoreRecoveryOptions{Operation: RecoveryRestore, Config: configPath, From: snapshot})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(summary, "after the snapshot") {
		t.Errorf("restore must say writes after the snapshot are lost; summary = %q", summary)
	}
	current, err := postgres.NewResources(pool).Resource(t.Context(), controlplane.ModelRoleAssignmentsKind)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 {
		t.Errorf("restored version = %d, want the snapshot's 1", current.Version)
	}
}

func TestPostgresRecoveryValidate(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, pool *pgxpool.Pool)
		wantErr string
	}{
		{name: "healthy store", setup: func(*testing.T, *pgxpool.Pool) {}},
		{name: "schema newer than this binary", wantErr: "newer than this binary", setup: func(t *testing.T, pool *pgxpool.Pool) {
			if _, err := pool.Exec(t.Context(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)", postgres.SupportedSchemaVersion()+1); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "stored value boot refuses", wantErr: "dispatch.trigger", setup: func(t *testing.T, pool *pgxpool.Pool) {
			putResource(t, pool, "scheduling-policy", `{"poll_interval":"1m","max_retries":3,"dispatch":{"trigger":"bogus"}}`, "bad")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, pool, _ := pgRecoveryFixture(t)
			tt.setup(t, pool)
			summary, err := RunStateStoreRecovery(t.Context(), StateStoreRecoveryOptions{Operation: RecoveryValidate, Config: configPath})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				if !strings.Contains(summary, fmt.Sprintf("schema version %d", postgres.SupportedSchemaVersion())) {
					t.Errorf("summary = %q, want the schema version", summary)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate = %v, want refusal containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPostgresRecoveryRollback(t *testing.T) {
	configPath, pool, url := pgRecoveryFixture(t)
	kind := controlplane.ModelRoleAssignmentsKind
	putResource(t, pool, kind, `{"implement":"anthropic/claude"}`, "first")
	putResource(t, pool, kind, `{"implement":"openai/gpt"}`, "second")
	options := StateStoreRecoveryOptions{Operation: RecoveryRollback, Config: configPath, Kind: kind}

	t.Run("refused while the State Store serves", func(t *testing.T) {
		holdRole(t, url, postgres.OwnerStateStore)
		if _, err := RunStateStoreRecovery(t.Context(), options); !errors.Is(err, postgres.ErrOwned) {
			t.Fatalf("rollback with the State Store serving = %v, want ErrOwned", err)
		}
	})

	if _, err := RunStateStoreRecovery(t.Context(), options); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	current, err := postgres.NewResources(pool).Resource(t.Context(), kind)
	if err != nil {
		t.Fatal(err)
	}
	if string(current.Value) != `{"implement": "anthropic/claude"}` && string(current.Value) != `{"implement":"anthropic/claude"}` {
		t.Errorf("value = %s, want the first revision's", current.Value)
	}
	if current.Version != 3 {
		t.Errorf("version = %d, want the rollback recorded as revision 3", current.Version)
	}
}
