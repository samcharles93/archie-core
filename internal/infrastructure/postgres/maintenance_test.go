package postgres

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestLibpqTargetKeepsThePasswordOffTheCommandLine(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantDB   string
		wantEnv  []string
		leakFree string
	}{
		{
			name:     "url with password",
			url:      "postgres://archie:s3cret@db.example:5432/archie?sslmode=require",
			wantDB:   "postgres://archie@db.example:5432/archie?sslmode=require",
			wantEnv:  []string{"PGPASSWORD=s3cret"},
			leakFree: "s3cret",
		},
		{
			name:   "url without password",
			url:    "postgresql://archie@db.example/archie",
			wantDB: "postgresql://archie@db.example/archie",
		},
		{
			name:   "key value connection string passes through",
			url:    "host=db.example dbname=archie",
			wantDB: "host=db.example dbname=archie",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, env := libpqTarget(tt.url)
			if db != tt.wantDB {
				t.Errorf("dbname = %q, want %q", db, tt.wantDB)
			}
			if !slices.Equal(env, tt.wantEnv) {
				t.Errorf("env = %q, want %q", env, tt.wantEnv)
			}
			if tt.leakFree != "" && strings.Contains(db, tt.leakFree) {
				t.Errorf("dbname %q carries the password", db)
			}
		})
	}
}

func TestToolArguments(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "dump is a whole-database custom-format archive",
			got:  dumpArgs("postgres://h/db", "/tmp/out.dump"),
			want: []string{"--format=custom", "--no-password", "--file=/tmp/out.dump", "--dbname=postgres://h/db"},
		},
		{
			name: "restore cleans, stops on the first error and is atomic",
			got:  restoreArgs("postgres://h/db", "/tmp/in.dump"),
			want: []string{"--clean", "--if-exists", "--single-transaction", "--exit-on-error", "--no-owner", "--no-password", "--dbname=postgres://h/db", "/tmp/in.dump"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("args = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestSnapshotTablesReadsTheArchiveList(t *testing.T) {
	list := `;
; Archive created at 2026-09-24 10:00:00 UTC
;
215; 1259 16390 TABLE public tasks archie
216; 1259 16391 SEQUENCE public tasks_id_seq archie
3401; 0 16390 TABLE DATA public tasks archie
217; 1259 16400 TABLE public goose_db_version archie
`
	got := snapshotTables(list)
	want := []string{"public.goose_db_version", "public.tasks"}
	if !slices.Equal(got, want) {
		t.Fatalf("snapshotTables = %q, want %q", got, want)
	}
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH; the integration test needs the PostgreSQL 18 client tools", tool)
		}
	}
}

// migratedURL is a migrated database and a pool on it.
func migratedURL(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	url := pgtest.URL(t)
	pool := openPool(t, url)
	if err := Migrate(t.Context(), pool, Migrations()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return url, pool
}

func titles(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), "SELECT title FROM tasks ORDER BY id")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		out = append(out, title)
	}
	return out
}

func enqueue(t *testing.T, pool *pgxpool.Pool, title string) {
	t.Helper()
	if _, err := New(pool).EnqueueChatTask(t.Context(), "acme", "widget", title, "body", "implement", ""); err != nil {
		t.Fatalf("enqueue %q: %v", title, err)
	}
}

// A restore after a forward migration is the case restore exists for: the
// tables the later migration added are not in the snapshot, so --clean alone
// would leave them behind for the next upgrade to trip over.
func TestBackupThenRestoreReturnsTheDatabaseToTheSnapshot(t *testing.T) {
	requireTools(t)
	url, pool := migratedURL(t)
	enqueue(t, pool, "before")
	before, err := SchemaVersion(t.Context(), pool)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.dump")
	if err := Backup(t.Context(), url, snapshot); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if leftovers, _ := filepath.Glob(snapshot + ".tmp*"); len(leftovers) != 0 {
		t.Errorf("backup left scratch files: %v", leftovers)
	}

	enqueue(t, pool, "after")
	if _, err := pool.Exec(t.Context(), "CREATE TABLE later_migration (id bigint)"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)", before+1); err != nil {
		t.Fatal(err)
	}

	if err := Restore(t.Context(), url, snapshot); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := titles(t, pool); !slices.Equal(got, []string{"before"}) {
		t.Errorf("tasks after restore = %q, want only the snapshot's", got)
	}
	var extra bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regclass('public.later_migration') IS NOT NULL").Scan(&extra); err != nil {
		t.Fatal(err)
	}
	if extra {
		t.Error("restore left a table the snapshot does not contain")
	}
	if after, err := SchemaVersion(t.Context(), pool); err != nil || after != before {
		t.Errorf("schema version after restore = %d, %v; want %d", after, err, before)
	}
	enqueue(t, pool, "writable")
}

func TestRestoreRefusesWhileAnyServiceIsServing(t *testing.T) {
	requireTools(t)
	for _, owner := range ServiceOwners {
		t.Run(owner, func(t *testing.T) {
			url, pool := migratedURL(t)
			enqueue(t, pool, "snapshot")
			snapshot := filepath.Join(t.TempDir(), "snapshot.dump")
			if err := Backup(t.Context(), url, snapshot); err != nil {
				t.Fatalf("Backup: %v", err)
			}
			enqueue(t, pool, "live")
			claimOther(t, url, owner, false, false)

			err := Restore(t.Context(), url, snapshot)
			if !errors.Is(err, ErrOwned) {
				t.Fatalf("Restore with %s serving = %v, want ErrOwned", owner, err)
			}
			if !strings.Contains(err.Error(), owner) {
				t.Errorf("refusal must name the serving role %q: %v", owner, err)
			}
			if got := titles(t, pool); !slices.Equal(got, []string{"snapshot", "live"}) {
				t.Errorf("refused restore changed the database: %q", got)
			}
			// Every claim the refused restore took is dropped again.
			for _, other := range ServiceOwners {
				if other == owner {
					continue
				}
				claim, err := AcquireOwnership(t.Context(), pool, other)
				if err != nil {
					t.Fatalf("%s still held after a refused restore: %v", other, err)
				}
				_ = claim.Release(t.Context())
			}
		})
	}
}

func TestRestoreRefusesWhatItCannotCheck(t *testing.T) {
	requireTools(t)
	url, pool := migratedURL(t)
	snapshot := filepath.Join(t.TempDir(), "snapshot.dump")
	if err := Backup(t.Context(), url, snapshot); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	notASnapshot := filepath.Join(t.TempDir(), "garbage.dump")
	if err := os.WriteFile(notASnapshot, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	enqueue(t, pool, "live")

	tests := []struct {
		name string
		url  string
		from string
	}{
		{name: "unreachable server", url: "postgres://archie:archie@127.0.0.1:1/none?connect_timeout=2", from: snapshot},
		{name: "not a snapshot", url: url, from: notASnapshot},
		{name: "missing snapshot", url: url, from: filepath.Join(t.TempDir(), "absent.dump")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Restore(t.Context(), tt.url, tt.from); err == nil {
				t.Fatal("Restore succeeded, want refusal")
			}
			if got := titles(t, pool); !slices.Equal(got, []string{"live"}) {
				t.Errorf("refused restore changed the database: %q", got)
			}
		})
	}
}

func TestBackupFailureKeepsThePreviousSnapshot(t *testing.T) {
	requireTools(t)
	out := filepath.Join(t.TempDir(), "snapshot.dump")
	if err := os.WriteFile(out, []byte("good snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Backup(t.Context(), "postgres://archie:archie@127.0.0.1:1/none?connect_timeout=2", out); err == nil {
		t.Fatal("Backup of an unreachable database succeeded")
	}
	if data, _ := os.ReadFile(out); string(data) != "good snapshot" {
		t.Errorf("failed backup replaced the previous snapshot: %q", data)
	}
	if leftovers, _ := filepath.Glob(out + ".tmp*"); len(leftovers) != 0 {
		t.Errorf("failed backup left scratch files: %v", leftovers)
	}
}

func TestCheckSchemaVersion(t *testing.T) {
	supported := SupportedSchemaVersion()
	if supported < 4 {
		t.Fatalf("SupportedSchemaVersion = %d, want the highest embedded migration", supported)
	}
	tests := []struct {
		name    string
		version int64
		wantErr error
	}{
		{name: "current", version: supported},
		{name: "older is upgradable", version: 1},
		{name: "newer than this binary", version: supported + 1, wantErr: ErrSchemaTooNew},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckSchemaVersion(tt.version); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckSchemaVersion(%d) = %v, want %v", tt.version, err, tt.wantErr)
			}
		})
	}
}
