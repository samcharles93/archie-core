// Package legacyfixture writes the legacy SQLite files an existing install
// holds, for tests of the one-time import and the boot gate that requires it.
package legacyfixture

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// StateStoreDDL, EDADDL and GatewayDDL apply the legacy schemas as their production openers leave
// them, columns added by ALTER TABLE included, with values in the layouts the
// production writers used.
const StateStoreDDL = `
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

// Secret is an encrypted binding secret envelope; it must arrive unchanged.
const Secret = "arcie-binding:v1:0123456789abcdef:AbC-_dEf012"

const EDADDL = `
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
INSERT INTO bindings VALUES ('rbind1', '2026-01-01 00:00:00.000Z', 'rmap1', 'b', 'o', 'r', '` + Secret + `', 'gh', 'armed', '2026-01-01 00:00:00.000Z', 1, 'implement');
INSERT INTO binding_dispatches VALUES ('rdisp1', 'rbind1', 1, 'rcap1', '2026-01-01 00:00:00.000Z', 4);
INSERT INTO playbook_dispatches VALUES ('rpb1', 'act', '2026-01-01 00:00:00.000Z', 'ev', 'pb', 'v1');
INSERT INTO tool_calls VALUES ('rtool1', NULL, 1, '2026-01-01 00:00:00.000Z', 12, '', '"exit 0"', 4, 'bash');
`

const GatewayDDL = `
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

// Install writes the three legacy files the configured db_path names in dir
// and returns their paths.
func Install(t testing.TB, dbPath string) (stateStore, eda, gateway string) {
	t.Helper()
	stateStore, eda, gateway = dbPath+"-tasks.sqlite", dbPath+"-eda.sqlite", dbPath+"-conversations.sqlite"
	Exec(t, stateStore, StateStoreDDL)
	Exec(t, eda, EDADDL)
	Exec(t, gateway, GatewayDDL)
	return stateStore, eda, gateway
}

// Exec runs stmt against the SQLite file at path, creating it if absent.
func Exec(t testing.TB, path, stmt string) {
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
