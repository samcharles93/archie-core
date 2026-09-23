package legacyread

import (
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The fixtures below apply the legacy schemas as their production openers
// leave them, columns added by ALTER TABLE included, without importing those
// openers.
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

INSERT INTO tasks (id, owner, repo, issue_number, review_cursor) VALUES (1, 'o', 'r', 7, 3), (4, 'o', 'r', 1000000000000000, 0);
INSERT INTO transitions (task_id, at, from_status, to_status) VALUES (1, '2026-01-01 00:00:00', 'queued', 'running');
INSERT INTO events (id, at, kind) VALUES
  (1, '2026-01-01T00:00:00.000000100Z', 'a'),
  (2, '2026-01-01T00:00:01.000000000Z', 'b');
INSERT INTO resources VALUES ('config', X'00ff10', 2, '2026-01-01T00:00:00Z');
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
INSERT INTO mappings VALUES ('rmap1', '2026-01-01', '[]', 'm', 'gh', '2026-01-01');
INSERT INTO bindings VALUES ('rbind1', '2026-01-01', 'rmap1', 'b', 'o', 'r', 'plaintext-secret', 'gh', 'armed', '2026-01-01', 1, 'implement');
INSERT INTO binding_dispatches VALUES ('rdisp1', 'rbind1', 1, 'rcap1', '2026-01-01', 4);
INSERT INTO playbook_dispatches VALUES ('rpb1', 'act', '2026-01-01', 'ev', 'pb', 'v1');
INSERT INTO tool_calls VALUES ('rtool1', '{}', 1, '2026-01-01', 12, '', '{}', 4, 'bash');
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

// fixture writes a legacy database built from ddl and returns its path. It
// is checkpointed into a single file so a hash covers all of its state.
func fixture(t *testing.T, name, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), ddl); err != nil {
		t.Fatalf("apply fixture DDL: %v", err)
	}
	return path
}

func exec(t *testing.T, path, stmt string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), stmt); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

func hashDir(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	sums := map[string][32]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		sums[e.Name()] = sha256.Sum256(b)
	}
	return sums
}

func openSource(t *testing.T, path string) *Source {
	t.Helper()
	src, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
}

// TestReadersDoNotMutateSources reads every table of all three stores and
// asserts every file in each store's directory is byte-for-byte unchanged,
// including the FTS5 index read.
func TestReadersDoNotMutateSources(t *testing.T) {
	stores := []struct {
		name   string
		ddl    string
		tables []Table
		rows   map[string]int
	}{
		{"archie.db", stateStoreDDL, StateStoreTables, map[string]int{"tasks": 2, "events": 2, "identity_subjects": 1}},
		{"archie.db-eda.sqlite", edaDDL, EDATables, map[string]int{"captures": 1, "bindings": 1, "tool_calls": 1}},
		{"archie.db-conversations.sqlite", gatewayDDL, GatewayTables, map[string]int{"messages": 2, "turns": 1}},
	}
	for _, s := range stores {
		t.Run(s.name, func(t *testing.T) {
			path := fixture(t, s.name, s.ddl)
			before := hashDir(t, filepath.Dir(path))
			src := openSource(t, path)
			for _, table := range s.tables {
				rows, err := src.Read(t.Context(), table)
				if err != nil {
					t.Fatalf("Read(%s): %v", table.Name, err)
				}
				if want, ok := s.rows[table.Name]; ok && len(rows.Rows) != want {
					t.Errorf("Read(%s) = %d rows, want %d", table.Name, len(rows.Rows), want)
				}
			}
			if s.name == "archie.db-conversations.sqlite" {
				if _, err := src.IndexedMessageIDs(t.Context()); err != nil {
					t.Fatalf("IndexedMessageIDs: %v", err)
				}
			}
			if err := src.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if after := hashDir(t, filepath.Dir(path)); !mapsEqual(before, after) {
				t.Fatalf("reading changed the source directory:\nbefore %x\nafter  %x", before, after)
			}
		})
	}
}

func mapsEqual(a, b map[string][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestOpenRefusesWrites pins the read-only open: a write through the
// source's connection is an error, not a silent mutation.
func TestOpenRefusesWrites(t *testing.T) {
	src := openSource(t, fixture(t, "archie.db", stateStoreDDL))
	if _, err := src.db.ExecContext(t.Context(), "DELETE FROM tasks"); err == nil {
		t.Fatal("DELETE through the read-only source succeeded")
	}
}

func TestReadCarriesAlteredColumnsAndBlobs(t *testing.T) {
	src := openSource(t, fixture(t, "archie.db", stateStoreDDL))
	tasks, err := src.Read(t.Context(), tableNamed(t, StateStoreTables, "tasks"))
	if err != nil {
		t.Fatalf("Read(tasks): %v", err)
	}
	if got := tasks.Value(0, "review_cursor"); got != int64(3) {
		t.Errorf("tasks[0].review_cursor = %#v, want 3 (an ALTER TABLE column)", got)
	}
	if got := tasks.Value(1, "issue_number"); got != int64(1_000_000_000_000_000) {
		t.Errorf("tasks[1].issue_number = %#v, want the chat seed", got)
	}
	res, err := src.Read(t.Context(), tableNamed(t, StateStoreTables, "resources"))
	if err != nil {
		t.Fatalf("Read(resources): %v", err)
	}
	if got, ok := res.Value(0, "value").([]byte); !ok || string(got) != "\x00\xff\x10" {
		t.Errorf("resources[0].value = %#v, want the raw bytes 00 ff 10", res.Value(0, "value"))
	}
}

func tableNamed(t *testing.T, tables []Table, name string) Table {
	t.Helper()
	i := slices.IndexFunc(tables, func(tb Table) bool { return tb.Name == name })
	if i < 0 {
		t.Fatalf("no table %q", name)
	}
	return tables[i]
}

func TestCompareKeys(t *testing.T) {
	tests := []struct {
		name           string
		source         []string
		target         []string
		missing, extra []string
	}{
		{name: "equal with the same gap", source: []string{"1", "2", "5"}, target: []string{"5", "1", "2"}},
		{name: "swallowed row", source: []string{"1", "2", "3"}, target: []string{"1", "3"}, missing: []string{"2"}},
		{name: "same count, different ids", source: []string{"1", "2"}, target: []string{"1", "9"}, missing: []string{"2"}, extra: []string{"9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareKeys(tt.source, tt.target)
			if !slices.Equal(got.Missing, tt.missing) || !slices.Equal(got.Extra, tt.extra) {
				t.Errorf("CompareKeys = %+v, want missing %v extra %v", got, tt.missing, tt.extra)
			}
		})
	}
}

func TestCompareOrder(t *testing.T) {
	if got := CompareOrder([]int64{1, 2, 3}, []int64{1, 2, 3}); got != nil {
		t.Errorf("CompareOrder(equal) = %+v, want nil", got)
	}
	got := CompareOrder([]int64{1, 2, 3}, []int64{1, 3, 2})
	if got == nil || got.Position != 1 || got.Source != 2 || got.Target != 3 {
		t.Errorf("CompareOrder(swapped) = %+v, want the first divergence at position 1 (2 vs 3)", got)
	}
	if got := CompareOrder([]int64{1, 2}, []int64{1}); got == nil || got.Position != 1 {
		t.Errorf("CompareOrder(short target) = %+v, want a divergence at position 1", got)
	}
}

// TestMicrosecondCollisions constructs D3.8's condition: two events whose
// instants differ only below a microsecond, with the later instant carrying
// the lower id, so Postgres's (at, id) order would reverse them.
func TestMicrosecondCollisions(t *testing.T) {
	path := fixture(t, "archie.db", stateStoreDDL)
	exec(t, path, `INSERT INTO events (id, at, kind) VALUES
		(10, '2026-02-01T00:00:00.000000900Z', 'later instant, lower id'),
		(11, '2026-02-01T00:00:00.000000100Z', 'earlier instant, higher id'),
		(12, '2026-02-01T00:00:05.000000100Z', 'same microsecond, ids in order'),
		(13, '2026-02-01T00:00:05.000000900Z', 'same microsecond, ids in order')`)
	src := openSource(t, path)
	got, err := src.MicrosecondCollisions(t.Context())
	if err != nil {
		t.Fatalf("MicrosecondCollisions: %v", err)
	}
	if len(got) != 1 || got[0].EarlierID != 11 || got[0].LaterID != 10 {
		t.Fatalf("MicrosecondCollisions = %+v, want exactly the pair (11 before 10)", got)
	}
}

func TestOrphans(t *testing.T) {
	path := fixture(t, "archie.db", stateStoreDDL)
	src := openSource(t, path)
	for _, fk := range IdentityForeignKeys {
		got, err := src.Orphans(t.Context(), fk)
		if err != nil || len(got) != 0 {
			t.Fatalf("Orphans(%s) on a clean fixture = %v, %v; want none", fk.Table, got, err)
		}
	}
	_ = src.Close()
	exec(t, path, `INSERT INTO identity_aliases VALUES ('ghost', 'id-missing')`)
	src = openSource(t, path)
	got, err := src.Orphans(t.Context(), IdentityForeignKeys[0])
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	if len(got) != 1 || got[0].Key != "ghost" || got[0].Parent != "id-missing" {
		t.Fatalf("Orphans(identity_aliases) = %+v, want the ghost alias", got)
	}
}

// TestDuplicateAliases pins the case-folding hazard: SQLite's NOCASE folds
// ASCII only, so two aliases differing in non-ASCII case coexist there and
// collide under Postgres's Unicode lower().
func TestDuplicateAliases(t *testing.T) {
	path := fixture(t, "archie.db", stateStoreDDL)
	exec(t, path, `INSERT INTO identity_aliases VALUES ('ÉMILE', 'id-1'), ('émile', 'id-1')`)
	src := openSource(t, path)
	got, err := src.DuplicateAliases(t.Context())
	if err != nil {
		t.Fatalf("DuplicateAliases: %v", err)
	}
	if len(got) != 1 || got[0].Key != "émile" || len(got[0].Values) != 2 {
		t.Fatalf("DuplicateAliases = %+v, want one group of two for émile", got)
	}
}

func TestDuplicateArmedBindings(t *testing.T) {
	path := fixture(t, "archie.db-eda.sqlite", edaDDL)
	src := openSource(t, path)
	if got, err := src.DuplicateBindings(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("DuplicateBindings on a clean fixture = %v, %v; want none", got, err)
	}
	_ = src.Close()
	exec(t, path, `INSERT INTO bindings VALUES ('rbind2', '', 'rmap1', 'b2', 'o', 'r', '', 'gh', 'pending_approval', '', 1, 'implement')`)
	src = openSource(t, path)
	got, err := src.DuplicateBindings(t.Context())
	if err != nil {
		t.Fatalf("DuplicateBindings: %v", err)
	}
	if len(got) != 1 || got[0].Key != "gh" || !slices.Equal(got[0].Values, []string{"rbind1", "rbind2"}) {
		t.Fatalf("DuplicateBindings = %+v, want source gh held by rbind1 and rbind2", got)
	}
}

func TestSequences(t *testing.T) {
	src := openSource(t, fixture(t, "archie.db", stateStoreDDL))
	got, err := src.Sequences(t.Context())
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	want := map[string]Sequence{
		"tasks":            {Table: "tasks", MaxID: 4, Next: 5},
		"transitions":      {Table: "transitions", MaxID: 1, Next: 2},
		"events":           {Table: "events", MaxID: 2, Next: 3},
		"resource_history": {Table: "resource_history", MaxID: 1, Next: 2},
		"identity_events":  {Table: "identity_events", MaxID: 1, Next: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("Sequences = %+v, want %d tables", got, len(want))
	}
	for _, s := range got {
		if want[s.Table] != s {
			t.Errorf("Sequences[%s] = %+v, want %+v", s.Table, s, want[s.Table])
		}
	}
}

// TestSequenceNextSkipsDeletedTop pins that Next comes from AUTOINCREMENT's
// high-water mark, not max(id): a deleted top row still reserves its id.
func TestSequenceNextSkipsDeletedTop(t *testing.T) {
	path := fixture(t, "archie.db", stateStoreDDL)
	exec(t, path, `DELETE FROM tasks WHERE id = 4`)
	src := openSource(t, path)
	got, err := src.Sequences(t.Context())
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	i := slices.IndexFunc(got, func(s Sequence) bool { return s.Table == "tasks" })
	if got[i].MaxID != 1 || got[i].Next != 5 {
		t.Fatalf("Sequences[tasks] = %+v, want max 1, next 5", got[i])
	}
}

func TestCompareBlobs(t *testing.T) {
	src := map[string][]byte{"config": {0, 0xff}, "routing": {1}}
	if got := CompareBlobs(src, map[string][]byte{"config": {0, 0xff}, "routing": {1}}); len(got) != 0 {
		t.Errorf("CompareBlobs(equal) = %v, want none", got)
	}
	got := CompareBlobs(src, map[string][]byte{"config": {0, 0xfe}})
	if !slices.Equal(got, []string{"config", "routing"}) {
		t.Errorf("CompareBlobs = %v, want config (changed) and routing (missing)", got)
	}
}

func TestIndexedMessageIDsFindsAStaleIndex(t *testing.T) {
	path := fixture(t, "archie.db-conversations.sqlite", gatewayDDL)
	exec(t, path, `DROP TRIGGER messages_ai; INSERT INTO messages (message_id, session_id, text, ts) VALUES ('m3', 's1', 'unindexed', 1002)`)
	src := openSource(t, path)
	indexed, err := src.IndexedMessageIDs(t.Context())
	if err != nil {
		t.Fatalf("IndexedMessageIDs: %v", err)
	}
	messages, err := src.Read(t.Context(), tableNamed(t, GatewayTables, "messages"))
	if err != nil {
		t.Fatalf("Read(messages): %v", err)
	}
	got := CompareKeys(messages.Keys(), indexed)
	if !slices.Equal(got.Missing, []string{"3"}) {
		t.Fatalf("CompareKeys(messages, index) = %+v, want message 3 missing from the index", got)
	}
}
