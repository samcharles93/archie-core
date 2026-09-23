package legacyimport

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// The fixtures apply the legacy schemas as their production openers leave
// them, columns added by ALTER TABLE included, with values in the layouts the
// production writers used.
const stateStoreDDL = `
CREATE TABLE tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, owner TEXT NOT NULL, repo TEXT NOT NULL,
  issue_number INTEGER NOT NULL, title TEXT DEFAULT '', body TEXT DEFAULT '', labels TEXT DEFAULT '',
  status TEXT DEFAULT 'queued', workflow TEXT DEFAULT '', stage TEXT DEFAULT '', branch TEXT DEFAULT '',
  plan TEXT DEFAULT '', notes TEXT DEFAULT '', pr_number INTEGER DEFAULT 0, tokens_used INTEGER DEFAULT 0,
  iterations INTEGER DEFAULT 0, attempt INTEGER DEFAULT 0, park_reason TEXT DEFAULT '',
  watch_comment_id INTEGER DEFAULT 0, park_class TEXT DEFAULT 'needs_human', remediation_rounds INTEGER DEFAULT 0,
  retry_count INTEGER DEFAULT 0, source TEXT DEFAULT 'forge', identity TEXT DEFAULT '', binding_id TEXT DEFAULT '',
  binding_version INTEGER DEFAULT 0, review_payload TEXT DEFAULT '', workflow_definition_version INTEGER DEFAULT 0,
  workflow_definition_digest TEXT DEFAULT '', workflow_definition_yaml TEXT DEFAULT '',
  created_at TEXT DEFAULT (datetime('now')), updated_at TEXT DEFAULT (datetime('now')));
ALTER TABLE tasks ADD COLUMN review_cursor INTEGER NOT NULL DEFAULT 0;
CREATE TABLE transitions (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL,
  at TEXT DEFAULT (datetime('now')), from_status TEXT NOT NULL, to_status TEXT NOT NULL, detail TEXT DEFAULT '');
CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL, kind TEXT NOT NULL,
  task_id INTEGER DEFAULT 0, repo TEXT DEFAULT '', issue INTEGER DEFAULT 0, workflow TEXT DEFAULT '',
  stage TEXT DEFAULT '', attempt INTEGER DEFAULT 0, actor_id TEXT DEFAULT '', actor_kind TEXT DEFAULT '',
  principal_id TEXT DEFAULT '', detail TEXT DEFAULT '', data TEXT DEFAULT '{}');
CREATE TABLE resources (kind TEXT PRIMARY KEY, value BLOB NOT NULL, version INTEGER NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE resource_history (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, value BLOB NOT NULL,
  version INTEGER NOT NULL, actor TEXT NOT NULL, source TEXT NOT NULL, request_id TEXT NOT NULL,
  expected_version INTEGER NOT NULL, current_version INTEGER NOT NULL, at TEXT NOT NULL);
CREATE TABLE channel_status (id TEXT PRIMARY KEY, name TEXT DEFAULT '', state TEXT DEFAULT '', detail TEXT DEFAULT '',
  configured INTEGER NOT NULL DEFAULT 0, reload_supported INTEGER NOT NULL DEFAULT 0, observed_at TEXT NOT NULL);
CREATE TABLE apply_status (process TEXT NOT NULL, kind TEXT NOT NULL, applied_version INTEGER DEFAULT 0,
  error TEXT DEFAULT '', reported_at TEXT NOT NULL, PRIMARY KEY (process, kind));
CREATE TABLE config_snapshot (id INTEGER PRIMARY KEY CHECK (id = 1), schema TEXT DEFAULT '', document TEXT DEFAULT '',
  published_at TEXT NOT NULL);
CREATE TABLE identities (id TEXT PRIMARY KEY, kind TEXT NOT NULL, display_name TEXT NOT NULL, lifecycle TEXT NOT NULL,
  version INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE identity_aliases (alias TEXT PRIMARY KEY COLLATE NOCASE, identity_id TEXT NOT NULL REFERENCES identities(id));
CREATE TABLE identity_events (id INTEGER PRIMARY KEY AUTOINCREMENT, identity_id TEXT NOT NULL, event_type TEXT NOT NULL,
  from_lifecycle TEXT NOT NULL, to_lifecycle TEXT NOT NULL, display_name TEXT NOT NULL, actor_id TEXT NOT NULL,
  source TEXT NOT NULL, request_id TEXT NOT NULL UNIQUE, at TEXT NOT NULL);
CREATE TABLE identity_subjects (issuer TEXT NOT NULL, subject TEXT NOT NULL,
  identity_id TEXT NOT NULL REFERENCES identities(id), bound_at TEXT NOT NULL, PRIMARY KEY (issuer, subject));

INSERT INTO tasks (id, owner, repo, issue_number, review_cursor, created_at, updated_at) VALUES
  (1, 'o', 'r', 7, 3, '2026-01-01 00:00:00', '2026-01-01 00:00:01'),
  (4, 'o', 'r', 1000000000000000, 0, '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
  (9, 'o', 'r', 8, 0, '2026-01-02 00:00:00', '2026-01-02 00:00:00');
DELETE FROM tasks WHERE id = 9;
INSERT INTO transitions (task_id, at, from_status, to_status) VALUES (1, '2026-01-01 00:00:00', 'queued', 'running');
INSERT INTO events (id, at, kind, data) VALUES
  (1, '2026-01-01T00:00:00.000000100Z', 'a', 'null'),
  (2, '2026-01-01T00:00:01.000000000Z', 'b', '{"k":1}'),
  (5, '2026-01-01T00:00:00.500000000Z', 'c', '{}');
INSERT INTO resources VALUES ('config', X'00ff10', 2, '2026-01-01T00:00:00.123456789Z');
INSERT INTO resource_history (kind, value, version, actor, source, request_id, expected_version, current_version, at)
  VALUES ('config', X'00ff10', 2, 'sam', 'ui', 'req-1', 1, 1, '2026-01-01T00:00:00Z');
INSERT INTO channel_status VALUES ('tg', 'Telegram', 'up', '', 1, 0, '2026-01-01T00:00:00Z');
INSERT INTO apply_status VALUES ('daemon', 'config', 2, '', '2026-01-01T00:00:00Z');
INSERT INTO config_snapshot VALUES (1, 's', '{}', '2026-01-01T00:00:00Z');
INSERT INTO identities VALUES ('id-1', 'agent', 'Archie', 'active', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO identity_aliases VALUES ('archie', 'id-1');
INSERT INTO identity_events (identity_id, event_type, from_lifecycle, to_lifecycle, display_name, actor_id, source, request_id, at)
  VALUES ('id-1', 'created', '', 'active', 'Archie', 'sam', 'boot', 'rq-1', '2026-01-01T00:00:00Z');
INSERT INTO identity_subjects VALUES ('https://issuer', 'sub-1', 'id-1', '2026-01-01T00:00:00Z');
`

// secret is an encrypted binding secret envelope; it must arrive unchanged.
const secret = "arcie-binding:v1:0123456789abcdef:AbC-_dEf012"

const edaDDL = `
CREATE TABLE captures (id TEXT PRIMARY KEY NOT NULL, authenticated BOOLEAN DEFAULT FALSE NOT NULL,
  body TEXT DEFAULT '' NOT NULL, content_type TEXT DEFAULT '' NOT NULL, headers JSON DEFAULT NULL,
  received_at TEXT DEFAULT '' NOT NULL, remote_addr TEXT DEFAULT '' NOT NULL, source TEXT DEFAULT '' NOT NULL);
CREATE TABLE mappings (id TEXT PRIMARY KEY NOT NULL, created_at TEXT DEFAULT '' NOT NULL, fields JSON DEFAULT NULL,
  name TEXT DEFAULT '' NOT NULL, source_hint TEXT DEFAULT '' NOT NULL, updated_at TEXT DEFAULT '' NOT NULL);
CREATE TABLE bindings (id TEXT PRIMARY KEY NOT NULL, created_at TEXT DEFAULT '' NOT NULL, mapping TEXT DEFAULT '' NOT NULL,
  name TEXT DEFAULT '' NOT NULL, owner TEXT DEFAULT '' NOT NULL, repo TEXT DEFAULT '' NOT NULL,
  secret TEXT DEFAULT '' NOT NULL, source TEXT DEFAULT '' NOT NULL, status TEXT DEFAULT '' NOT NULL,
  updated_at TEXT DEFAULT '' NOT NULL, version NUMERIC DEFAULT 0 NOT NULL, workflow TEXT DEFAULT '' NOT NULL);
CREATE TABLE binding_dispatches (id TEXT PRIMARY KEY NOT NULL, binding TEXT DEFAULT '' NOT NULL,
  binding_version NUMERIC DEFAULT 0 NOT NULL, capture TEXT DEFAULT '' NOT NULL, dispatched_at TEXT DEFAULT '' NOT NULL,
  task_id NUMERIC DEFAULT 0 NOT NULL);
CREATE UNIQUE INDEX idx_binding_dispatch_once ON binding_dispatches (binding, capture);
CREATE TABLE playbook_dispatches (id TEXT PRIMARY KEY NOT NULL, action_id TEXT DEFAULT '' NOT NULL,
  dispatched_at TEXT DEFAULT '' NOT NULL, event_id TEXT DEFAULT '' NOT NULL, playbook_id TEXT DEFAULT '' NOT NULL,
  playbook_version TEXT DEFAULT '' NOT NULL);
CREATE TABLE tool_calls (id TEXT PRIMARY KEY NOT NULL, args JSON DEFAULT NULL, attempt NUMERIC DEFAULT 0 NOT NULL,
  called_at TEXT DEFAULT '' NOT NULL, duration_ms NUMERIC DEFAULT 0 NOT NULL, error TEXT DEFAULT '' NOT NULL,
  result JSON DEFAULT NULL, task_id NUMERIC DEFAULT 0 NOT NULL, tool TEXT DEFAULT '' NOT NULL);

INSERT INTO captures VALUES ('rcap1', 1, '{}', 'application/json', '{"x":["1"]}', '2026-01-01T00:00:00.000000000Z', '127.0.0.1', 'gh');
INSERT INTO captures VALUES ('rcap2', 0, '', '', NULL, '2026-01-01T00:00:01.000000000Z', '', 'gh');
INSERT INTO mappings VALUES ('rmap1', '2026-01-01 00:00:00.000Z', '[]', 'm', 'gh', '2026-01-01 00:00:00.000Z');
INSERT INTO bindings VALUES ('rbind1', '2026-01-01 00:00:00.000Z', 'rmap1', 'b', 'o', 'r', '` + secret + `', 'gh', 'armed', '2026-01-01 00:00:00.000Z', 1, 'implement');
INSERT INTO binding_dispatches VALUES ('rdisp1', 'rbind1', 1, 'rcap1', '2026-01-01 00:00:00.000Z', 4);
INSERT INTO playbook_dispatches VALUES ('rpb1', 'act', '2026-01-01 00:00:00.000Z', 'ev', 'pb', 'v1');
INSERT INTO tool_calls VALUES ('rtool1', NULL, 1, '2026-01-01 00:00:00.000Z', 12, '', '"exit 0"', 4, 'bash');
`

const gatewayDDL = `
CREATE TABLE sessions (session_id TEXT PRIMARY KEY, platform TEXT NOT NULL DEFAULT '', bot_user TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '', thread_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
  parent_session_id TEXT NOT NULL DEFAULT '', branch_name TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0, last_active_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
  source_id TEXT NOT NULL DEFAULT '', sender TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '', ts INTEGER NOT NULL,
  UNIQUE(session_id, message_id));
ALTER TABLE messages ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN sender_id TEXT NOT NULL DEFAULT '';
CREATE VIRTUAL TABLE messages_fts USING fts5(sender, text, content='messages', content_rowid='id');
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, sender, text) VALUES (new.id, new.sender, new.text);
END;
CREATE TABLE turns (turn_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, owner_id TEXT NOT NULL DEFAULT '',
  input_message_id TEXT NOT NULL DEFAULT '', assistant_message_id TEXT NOT NULL DEFAULT '',
  partial_text TEXT NOT NULL DEFAULT '', response_text TEXT NOT NULL DEFAULT '', tool_calls TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);

INSERT INTO sessions VALUES ('s1', 'telegram', 'bot', 'c1', '', 'Chat', '', '', 1000, 2000);
INSERT INTO messages (message_id, session_id, sender, text, ts, role) VALUES ('m1', 's1', 'sam', 'hello there', 1000, 'user');
INSERT INTO messages (message_id, session_id, sender, text, ts, role) VALUES ('m2', 's1', 'archie', 'hi', 1001, 'assistant');
INSERT INTO turns VALUES ('t1', 's1', '', 'done', 1, '', 'm1', 'm2', '', 'hi', '[]', '', 1000, 1001);
`

// install writes the three legacy files the way an existing install holds
// them and returns their paths. mutate runs extra statements against a file
// before the import sees it.
func install(t *testing.T, mutate map[string]string) Sources {
	t.Helper()
	dir := t.TempDir()
	src := Sources{
		StateStore: filepath.Join(dir, "archie.db-tasks.sqlite"),
		EDA:        filepath.Join(dir, "archie.db-eda.sqlite"),
		Gateway:    filepath.Join(dir, "archie.db-conversations.sqlite"),
	}
	for path, ddl := range map[string]string{src.StateStore: stateStoreDDL, src.EDA: edaDDL, src.Gateway: gatewayDDL} {
		sqliteExec(t, path, ddl)
	}
	for path, stmt := range mutate {
		switch path {
		case "state":
			sqliteExec(t, src.StateStore, stmt)
		case "eda":
			sqliteExec(t, src.EDA, stmt)
		case "gateway":
			sqliteExec(t, src.Gateway, stmt)
		}
	}
	return src
}

func sqliteExec(t *testing.T, path, stmt string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), stmt); err != nil {
		t.Fatalf("fixture %s: %v", filepath.Base(path), err)
	}
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
	if gotSecret != secret {
		t.Errorf("binding secret = %q, want %q", gotSecret, secret)
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
