package legacyimport

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyimport/legacyfixture"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// install writes the three legacy files the way an existing install holds
// them and returns their paths. mutate runs extra statements against a file
// before the import sees it.
func install(t *testing.T, mutate map[string]string) Sources {
	t.Helper()
	var src Sources
	src.StateStore, src.EDA, src.Gateway = legacyfixture.Install(t, filepath.Join(t.TempDir(), "archie.db"))
	for path, stmt := range mutate {
		switch path {
		case "state":
			legacyfixture.Exec(t, src.StateStore, stmt)
		case "eda":
			legacyfixture.Exec(t, src.EDA, stmt)
		case "gateway":
			legacyfixture.Exec(t, src.Gateway, stmt)
		}
	}
	return src
}

func hashFiles(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	out := map[string][32]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = sha256.Sum256(b)
	}
	return out
}

func target(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.Open(t.Context(), pgtest.URL(t))
	if err != nil {
		t.Fatalf("postgres.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := target(t)
	if err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestImportCopiesEverySourceAndLeavesThemUntouched(t *testing.T) {
	src := install(t, nil)
	before := hashFiles(t, filepath.Dir(src.StateStore))
	pool := target(t)

	report, err := Import(t.Context(), pool, src)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if after := hashFiles(t, filepath.Dir(src.StateStore)); len(after) != len(before) {
		t.Fatalf("source directory changed: %d files before, %d after", len(before), len(after))
	} else {
		for name, sum := range before {
			if after[name] != sum {
				t.Errorf("source %s changed during import", name)
			}
		}
	}

	done, err := postgres.ImportComplete(t.Context(), pool)
	if err != nil || !done {
		t.Fatalf("ImportComplete = %v, %v; want true", done, err)
	}

	counts := map[string]int{
		"tasks": 2, "transitions": 1, "events": 3, "resources": 1, "resource_history": 1,
		"channel_status": 1, "apply_status": 1, "config_snapshot": 1, "identities": 1,
		"identity_aliases": 1, "identity_events": 1, "identity_subjects": 1,
		"captures": 2, "mappings": 1, "bindings": 1, "binding_dispatches": 1, "playbook_dispatches": 1,
		"tool_calls": 1, "sessions": 1, "messages": 2, "turns": 1,
	}
	for table, want := range counts {
		if got := count(t, pool, table); got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
		if got := report.Rows[table]; got != want {
			t.Errorf("report rows[%s] = %d, want %d", table, got, want)
		}
	}

	ctx := t.Context()
	var (
		reviewCursor int64
		value        []byte
		gotSecret    string
		headers      string
		result       string
		configured   bool
	)
	checks := []struct {
		query string
		dest  any
	}{
		{"SELECT review_cursor FROM tasks WHERE id = 1", &reviewCursor},
		{"SELECT value FROM resource_history WHERE id = 1", &value},
		{"SELECT secret FROM bindings WHERE id = 'rbind1'", &gotSecret},
		{"SELECT headers FROM captures WHERE id = 'rcap2'", &headers},
		{"SELECT result FROM tool_calls WHERE id = 'rtool1'", &result},
		{"SELECT configured FROM channel_status WHERE id = 'tg'", &configured},
	}
	for _, c := range checks {
		if err := pool.QueryRow(ctx, c.query).Scan(c.dest); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
	}
	if reviewCursor != 3 {
		t.Errorf("review_cursor = %d, want 3", reviewCursor)
	}
	if !bytes.Equal(value, []byte{0x00, 0xff, 0x10}) {
		t.Errorf("resource_history.value = %x, want 00ff10", value)
	}
	if gotSecret != legacyfixture.Secret {
		t.Errorf("binding secret = %q, want %q", gotSecret, legacyfixture.Secret)
	}
	if headers != "" || result != "exit 0" || !configured {
		t.Errorf("headers %q, result %q, configured %v; want \"\", \"exit 0\", true", headers, result, configured)
	}

	// Event order is (at, id) as the source had it, and the next event id
	// follows AUTOINCREMENT's high-water mark rather than reusing one.
	rows, err := pool.Query(ctx, "SELECT id FROM events ORDER BY at, id")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var order []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		order = append(order, id)
	}
	if want := []int64{1, 5, 2}; !equalInts(order, want) {
		t.Errorf("event order = %v, want %v", order, want)
	}
	nextIDs := map[string]int64{"tasks": 10, "events": 6, "messages": 3}
	for table, want := range nextIDs {
		var got int64
		if err := pool.QueryRow(ctx, "SELECT nextval(pg_get_serial_sequence($1, 'id'))", table).Scan(&got); err != nil {
			t.Fatalf("nextval %s: %v", table, err)
		}
		if got != want {
			t.Errorf("next %s id = %d, want %d", table, got, want)
		}
	}

	var hits int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE search @@ to_tsquery('simple', 'hello')").Scan(&hits); err != nil {
		t.Fatalf("search: %v", err)
	}
	if hits != 1 {
		t.Errorf("search hits for an imported message = %d, want 1", hits)
	}
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestImportRefuses(t *testing.T) {
	tests := []struct {
		name   string
		mutate map[string]string
		seed   func(t *testing.T, pool *pgxpool.Pool)
		want   []string
		is     error
	}{
		{
			name: "non-empty target",
			seed: func(t *testing.T, pool *pgxpool.Pool) {
				mustExec(t, pool, "INSERT INTO transitions (task_id, from_status, to_status) VALUES (1, 'a', 'b')")
			},
			want: []string{"transitions", "not empty"},
		},
		{
			name: "completed import",
			seed: func(t *testing.T, pool *pgxpool.Pool) {
				mustExec(t, pool, "INSERT INTO import_completion (id, sources, report) VALUES (1, 's', 'r')")
			},
			want: []string{"already"},
		},
		{
			name: "live state store",
			seed: func(t *testing.T, pool *pgxpool.Pool) { holdOwnership(t, pool, postgres.OwnerStateStore) },
			want: []string{"state-store"},
			is:   postgres.ErrOwned,
		},
		{
			name: "live gateway",
			seed: func(t *testing.T, pool *pgxpool.Pool) { holdOwnership(t, pool, postgres.OwnerGateway) },
			want: []string{"gateway"},
			is:   postgres.ErrOwned,
		},
		{
			name:   "orphaned alias",
			mutate: map[string]string{"state": "INSERT INTO identity_aliases VALUES ('ghost', 'id-404')"},
			want:   []string{"identity_aliases", "ghost", "id-404"},
		},
		{
			name:   "duplicate bindings",
			mutate: map[string]string{"eda": "INSERT INTO bindings VALUES ('rbind2', '2026-01-01 00:00:00.000Z', '', 'b2', 'o', 'r', '', 'gh', 'pending_approval', '2026-01-01 00:00:00.000Z', 1, 'implement')"},
			want:   []string{"bindings", "gh", "rbind1", "rbind2"},
		},
		{
			name:   "microsecond collision",
			mutate: map[string]string{"state": "INSERT INTO events (id, at, kind) VALUES (7, '2026-01-01T00:00:00.000000050Z', 'd')"},
			want:   []string{"events", "7", "microsecond"},
		},
		{
			name:   "unparseable timestamp",
			mutate: map[string]string{"eda": "UPDATE captures SET received_at = 'yesterday' WHERE id = 'rcap2'"},
			want:   []string{"captures", "rcap2", "yesterday", "received_at"},
		},
		{
			name:   "null in a required column",
			mutate: map[string]string{"state": "UPDATE tasks SET title = NULL WHERE id = 4"},
			want:   []string{"tasks", "4", "title", "NULL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := install(t, tt.mutate)
			pool := migrated(t)
			if tt.seed != nil {
				tt.seed(t, pool)
			}
			_, err := Import(t.Context(), pool, src)
			if err == nil {
				t.Fatal("Import succeeded, want a refusal")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("Import error %v, want %v", err, tt.is)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q does not name %q", err, w)
				}
			}
			// Nothing of the import survives a refusal: the store stays
			// unserveable rather than half-imported.
			if got := count(t, pool, "tasks"); got != 0 {
				t.Errorf("tasks rows after refusal = %d, want 0", got)
			}
			if got := count(t, pool, "import_completion"); tt.name != "completed import" && got != 0 {
				t.Errorf("completion records after refusal = %d, want 0", got)
			}
		})
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, stmt string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// holdOwnership claims name from a separate pool, as a live service would.
func holdOwnership(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	other, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	t.Cleanup(other.Close)
	claim, err := postgres.AcquireOwnership(t.Context(), other, name)
	if err != nil {
		t.Fatalf("claim %s: %v", name, err)
	}
	t.Cleanup(func() { _ = claim.Release(t.Context()) })
}

// TestSiblingDomainDoesNotBlock pins that "non-empty" is per domain: a
// domain with no legacy source is not imported, so rows in its tables do not
// refuse the others.
func TestSiblingDomainDoesNotBlock(t *testing.T) {
	src := install(t, nil)
	src.Gateway = filepath.Join(t.TempDir(), "absent.sqlite")
	pool := migrated(t)
	mustExec(t, pool, "INSERT INTO sessions (session_id) VALUES ('live')")
	if _, err := Import(t.Context(), pool, src); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := count(t, pool, "tasks"); got != 2 {
		t.Fatalf("tasks rows = %d, want 2", got)
	}
}

func TestImportRefusesWithNoSources(t *testing.T) {
	dir := t.TempDir()
	src := Sources{StateStore: filepath.Join(dir, "a"), EDA: filepath.Join(dir, "b"), Gateway: filepath.Join(dir, "c")}
	if _, err := Import(t.Context(), migrated(t), src); err == nil || !strings.Contains(err.Error(), "no legacy") {
		t.Fatalf("Import = %v, want a refusal naming no legacy sources", err)
	}
}

func TestHasLegacyData(t *testing.T) {
	dir := t.TempDir()
	absent := Sources{StateStore: filepath.Join(dir, "a"), EDA: filepath.Join(dir, "b"), Gateway: filepath.Join(dir, "c")}
	onlyGateway := absent
	onlyGateway.Gateway = install(t, nil).Gateway
	tests := []struct {
		name string
		src  Sources
		want bool
	}{
		{"fresh install", absent, false},
		{"one source with rows", onlyGateway, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HasLegacyData(t.Context(), tt.src)
			if err != nil {
				t.Fatalf("HasLegacyData: %v", err)
			}
			if got != tt.want {
				t.Fatalf("HasLegacyData = %v, want %v", got, tt.want)
			}
		})
	}
}
