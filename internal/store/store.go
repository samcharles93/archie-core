// Package store is archied's SQLite state: one row per task (a GitHub
// issue picked up for work), with every lifecycle transition recorded.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type Store struct {
	db *sql.DB
}

// taskSchemaVersion is the newest schema this binary opens. ValidateFile
// refuses a database whose version is newer, so a binary and the file it
// opens move together.
const taskSchemaVersion = 4

// OpenOption configures the store at open time.
type OpenOption func(*openOptions)

type openOptions struct{}

// Open opens (creating if needed) the SQLite database and its schema.
func Open(ctx context.Context, path string, opts ...OpenOption) (*Store, error) {
	var o openOptions
	for _, opt := range opts {
		opt(&o)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema+eventsSchema+configSnapshotSchema+channelStatusSchema+applyStatusSchema+resourcesSchema+identitiesSchema); err != nil {
		return nil, errors.Join(fmt.Errorf("store: init schema: %w", err), db.Close())
	}
	if err := migrateTasks(ctx, db); err != nil {
		return nil, errors.Join(fmt.Errorf("store: migrate: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

func sqliteDSN(path string) string {
	separator := "?"
	if strings.HasSuffix(path, "?") || strings.HasSuffix(path, "&") {
		separator = ""
	} else if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func migrateTasks(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > taskSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, taskSchemaVersion)
	}
	columns, err := taskColumns(ctx, tx)
	if err != nil {
		return err
	}
	// events is created by `CREATE TABLE IF NOT EXISTS` (eventsSchema), which
	// is a no-op against a database that already has the table. On an existing
	// archie.db this read plus the `events` arm below is therefore the ONLY
	// thing that adds events.attempt; without it the first insert fails with
	// `table events has no column named attempt`.
	eventsColumns, err := tableColumns(ctx, tx, "events")
	if err != nil {
		return err
	}

	migrations := []struct {
		table  string
		column string
		sql    string
	}{
		{"tasks", "watch_comment_id", `ALTER TABLE tasks ADD COLUMN watch_comment_id INTEGER NOT NULL DEFAULT 0`},
		{"tasks", "review_cursor", `ALTER TABLE tasks ADD COLUMN review_cursor INTEGER NOT NULL DEFAULT 0`},
		{"tasks", "park_class", `ALTER TABLE tasks ADD COLUMN park_class TEXT NOT NULL DEFAULT 'needs_human'`},
		{"tasks", "remediation_rounds", `ALTER TABLE tasks ADD COLUMN remediation_rounds INTEGER NOT NULL DEFAULT 0`},
		{"tasks", "retry_count", `ALTER TABLE tasks ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`},
		{"tasks", "source", `ALTER TABLE tasks ADD COLUMN source TEXT NOT NULL DEFAULT 'forge'`},
		{"tasks", "identity", `ALTER TABLE tasks ADD COLUMN identity TEXT NOT NULL DEFAULT ''`},
		{"tasks", "binding_id", `ALTER TABLE tasks ADD COLUMN binding_id TEXT NOT NULL DEFAULT ''`},
		{"tasks", "binding_version", `ALTER TABLE tasks ADD COLUMN binding_version INTEGER NOT NULL DEFAULT 0`},
		{"tasks", "review_payload", `ALTER TABLE tasks ADD COLUMN review_payload TEXT NOT NULL DEFAULT ''`},
		{"tasks", "workflow_definition_version", `ALTER TABLE tasks ADD COLUMN workflow_definition_version INTEGER NOT NULL DEFAULT 0`},
		{"tasks", "workflow_definition_digest", `ALTER TABLE tasks ADD COLUMN workflow_definition_digest TEXT NOT NULL DEFAULT ''`},
		{"tasks", "workflow_definition_yaml", `ALTER TABLE tasks ADD COLUMN workflow_definition_yaml TEXT NOT NULL DEFAULT ''`},
		{"events", "attempt", `ALTER TABLE events ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0`},
		{"events", "actor_id", `ALTER TABLE events ADD COLUMN actor_id TEXT NOT NULL DEFAULT ''`},
		{"events", "actor_kind", `ALTER TABLE events ADD COLUMN actor_kind TEXT NOT NULL DEFAULT ''`},
		{"events", "principal_id", `ALTER TABLE events ADD COLUMN principal_id TEXT NOT NULL DEFAULT ''`},
	}
	for _, migration := range migrations {
		// Only tasks and events remain here; the event-capture tables moved
		// to internal/infrastructure/edastore, which owns their schema.
		present := columns
		if migration.table == "events" {
			present = eventsColumns
		}
		if present[migration.column] {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return err
		}
	}
	if err := migrateResourceHistoryToPerKindRequestIDs(ctx, tx); err != nil {
		return err
	}
	if err := migrateEventTimestamps(ctx, tx); err != nil {
		return err
	}
	return finishTaskMigration(ctx, tx, columns)
}

// migrateResourceHistoryToPerKindRequestIDs is the v4 step: the history
// table's global request_id UNIQUE becomes per-kind uniqueness, the key the
// resource write path actually dedups on (resourceByRequest). SQLite cannot
// drop a column constraint, so the table is rebuilt in place; a global
// constraint is strictly stricter than the per-kind one, so no existing row
// can fail the new shape. Detection is the auto-index the old column
// constraint created -- sqlite_master carries it with a NULL sql -- which the
// rebuilt table leaves no copy of, so the step is a no-op on a database that
// already has the v4 shape (including one resourcesSchema created fresh).
func migrateResourceHistoryToPerKindRequestIDs(ctx context.Context, tx *sql.Tx) error {
	var autoIndexes int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='resource_history' AND sql IS NULL`).Scan(&autoIndexes); err != nil {
		return err
	}
	if autoIndexes == 0 {
		return nil
	}
	for _, statement := range []string{
		`CREATE TABLE resource_history_per_kind_request (
			id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, value BLOB NOT NULL,
			version INTEGER NOT NULL, actor TEXT NOT NULL, source TEXT NOT NULL,
			request_id TEXT NOT NULL, expected_version INTEGER NOT NULL,
			current_version INTEGER NOT NULL, at TEXT NOT NULL
		)`,
		`INSERT INTO resource_history_per_kind_request (id,kind,value,version,actor,source,request_id,expected_version,current_version,at)
			SELECT id,kind,value,version,actor,source,request_id,expected_version,current_version,at FROM resource_history`,
		`DROP TABLE resource_history`,
		`ALTER TABLE resource_history_per_kind_request RENAME TO resource_history`,
		`CREATE INDEX IF NOT EXISTS idx_resource_history_kind_version ON resource_history(kind, version)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_history_kind_request ON resource_history(kind, request_id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild resource_history for per-kind request IDs: %w", err)
		}
	}
	return nil
}

// migrateEventTimestamps normalises the events.at column to the fixed-width
// layout the EventsSince cursor sorts on. Rows written before the cursor
// change used time.RFC3339Nano, which trims trailing zeros, so a whole-second
// row (20 chars) and a full-precision row (30 chars) would compare out of
// chronological order as strings. The ALTER-only migration arms never UPDATE
// values, so this is a separate idempotent step (the v4 precedent): it
// re-evaluates on every open, detects an already-normalised file by length,
// and re-runs cleanly on one that has never been migrated.
func migrateEventTimestamps(ctx context.Context, tx *sql.Tx) error {
	updates, err := scanLegacyEventTimestamps(ctx, tx)
	if err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE events SET at = ? WHERE id = ?`, u.at, u.id); err != nil {
			return err
		}
	}
	return nil
}

// eventTimestampUpdate is one event row whose at predates the fixed-width
// layout, re-rendered into that layout.
type eventTimestampUpdate struct {
	id int64
	at string
}

// scanLegacyEventTimestamps reads every event row whose at predates the
// fixed-width layout, parses it, and re-renders it. The read cursor is closed
// by the defer before this function returns, so the caller's UPDATEs run on a
// free connection -- the store opens with SetMaxOpenConns(1), and a still-open
// read cursor would hold that single connection and deadlock the writes.
func scanLegacyEventTimestamps(ctx context.Context, tx *sql.Tx) ([]eventTimestampUpdate, error) {
	// A fixed-width row is exactly len(EventCursorLayout) characters; anything
	// else predates the layout and needs re-formatting.
	fixed := strings.Repeat("?", len(storecontract.EventCursorLayout))
	rows, err := tx.QueryContext(ctx, `SELECT id, at FROM events WHERE at NOT GLOB '`+fixed+`'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var updates []eventTimestampUpdate
	for rows.Next() {
		var u eventTimestampUpdate
		if err := rows.Scan(&u.id, &u.at); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, u.at)
		if err != nil {
			return nil, fmt.Errorf("normalise event timestamp %q: %w", u.at, err)
		}
		u.at = parsed.UTC().Format(storecontract.EventCursorLayout)
		updates = append(updates, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return updates, nil
}

func finishTaskMigration(ctx context.Context, tx *sql.Tx, columns map[string]bool) error {
	if columns["owner"] && columns["repo"] && columns["pr_number"] {
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_tasks_pr ON tasks(owner, repo, pr_number)`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, taskSchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func taskColumns(ctx context.Context, tx *sql.Tx) (_ map[string]bool, retErr error) {
	return tableColumns(ctx, tx, "tasks")
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) (_ map[string]bool, retErr error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	owner         TEXT NOT NULL,
	repo          TEXT NOT NULL,
	issue_number  INTEGER NOT NULL,
	title         TEXT NOT NULL DEFAULT '',
	body          TEXT NOT NULL DEFAULT '',
	labels        TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL DEFAULT '` + workflow.StatusQueued + `',
	workflow      TEXT NOT NULL DEFAULT '',
	stage         TEXT NOT NULL DEFAULT '',
	branch        TEXT NOT NULL DEFAULT '',
	plan          TEXT NOT NULL DEFAULT '',
	notes         TEXT NOT NULL DEFAULT '',
	pr_number     INTEGER NOT NULL DEFAULT 0,
	tokens_used   INTEGER NOT NULL DEFAULT 0,
	iterations    INTEGER NOT NULL DEFAULT 0,
	attempt       INTEGER NOT NULL DEFAULT 0,
	park_reason   TEXT NOT NULL DEFAULT '',
	watch_comment_id INTEGER NOT NULL DEFAULT 0,
		park_class TEXT NOT NULL DEFAULT 'needs_human',
		remediation_rounds INTEGER NOT NULL DEFAULT 0,
	retry_count   INTEGER NOT NULL DEFAULT 0,
	source        TEXT NOT NULL DEFAULT 'forge',
	identity      TEXT NOT NULL DEFAULT '',
	-- binding_id carries the id of the edastore binding that produced this
	-- task, or "" for a task nobody bound. It is TEXT because bindings are
	-- PocketBase records; a database created before that stores the same
	-- values in an INTEGER-affinity column, which SQLite keeps as TEXT.
	binding_id    TEXT NOT NULL DEFAULT '',
	binding_version INTEGER NOT NULL DEFAULT 0,
	review_payload TEXT NOT NULL DEFAULT '',
	workflow_definition_version INTEGER NOT NULL DEFAULT 0,
	workflow_definition_digest TEXT NOT NULL DEFAULT '',
	workflow_definition_yaml TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(owner, repo, issue_number)
);
CREATE TABLE IF NOT EXISTS transitions (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id  INTEGER NOT NULL,
	at       TEXT NOT NULL DEFAULT (datetime('now')),
	from_status TEXT NOT NULL,
	to_status   TEXT NOT NULL,
	detail   TEXT NOT NULL DEFAULT ''
);
`

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB so tests outside the store
// package can drive fixtures that the public surface does not (yet)
// cover -- e.g. advancing a binding draft -> pending_approval without
// going through the operator edit flow. Production callers must NOT
// use this; the structured methods above are the contract.
func (s *Store) DB() *sql.DB { return s.db }

// OpenTest opens an in-memory SQLite store suitable for tests.
func OpenTest(t interface {
	TempDir() string
	Context() context.Context
},
) *Store {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		panic(err)
	}
	return s
}

// EnqueueIssue inserts a new queued task for the issue; returns false if
// the issue is already tracked (the idempotency key is owner/repo/number).
// The task's Source defaults to "forge" (the SQLite column default).
// identity is the archie identity whose forge poll discovered this issue
// (empty for single-identity deployments); it scopes which identity's
// forge client, worktree manager, and config downstream processing uses.
func (s *Store) EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (owner, repo, issue_number, title, body, labels, identity)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner, repo, issue_number) DO NOTHING`,
		owner, repo, number, title, body, labels, identity)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// EnqueueChatTask inserts a new queued task with Source "chat" and no
// backing forge issue, returning the full created row (with its real
// database ID). The store allocates the synthetic issue number durably so
// multiple processes sharing the database cannot generate the same value.
// Workflow stages and daemon reconciliation must check workflow.Task.IsForgeBacked()
// before treating it as a real forge issue number.
func (s *Store) EnqueueChatTask(ctx context.Context, owner, repo, title, body, wf, identity string) (*workflow.Task, error) {
	const syntheticIssueNumberBase = 1_000_000_000_000_000
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO tasks (owner, repo, issue_number, title, body, labels, workflow, source, identity)
		VALUES (?, ?, COALESCE((
			SELECT MAX(issue_number) FROM tasks
			WHERE owner = ? AND repo = ? AND source = 'chat'
		), ?) + 1, ?, ?, 'chat', ?, 'chat', ?)
		RETURNING id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, binding_id, binding_version, review_payload,
			workflow_definition_version, workflow_definition_digest, workflow_definition_yaml, park_class, remediation_rounds, created_at, updated_at`,
		owner, repo, owner, repo, syntheticIssueNumberBase-1, title, body, wf, identity)
	return scanTask(row)
}

// EnqueueBindingTask enqueues a task triggered by a playbook binding
// dispatch (t2db.4 Phase B). It builds the row through EnqueueChatTask
// (so the synthetic issue-number allocator stays the single source of
// truth for chat-sourced tasks) and then stamps binding_id and
// binding_version on the new row in a second statement.
//
// A crash between the two writes leaves the task without provenance
// (binding_id == 0); the binding_dispatches ledger row written by the
// dispatch loop still records the (binding, capture, task) triple, so
// a future repair pass could backfill. This is the same
// best-effort-provenance pattern the rest of the task lifecycle uses
// for fields added after the row's primary insert.
func (s *Store) EnqueueBindingTask(ctx context.Context, owner, repo, title, body, wf, identity, bindingID string, bindingVersion int) (*workflow.Task, error) {
	t, err := s.EnqueueChatTask(ctx, owner, repo, title, body, wf, identity)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET binding_id=?, binding_version=? WHERE id=?`,
		bindingID, bindingVersion, t.ID); err != nil {
		return nil, fmt.Errorf("store: stamp binding provenance: %w", err)
	}
	t.BindingID = bindingID
	t.BindingVersion = bindingVersion
	return t, nil
}

// ClaimNext atomically moves the oldest queued task to running and
// returns it; nil when the queue is empty.
func (s *Store) ClaimNext(ctx context.Context) (*workflow.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE tasks SET status=?, attempt=attempt+1, updated_at=datetime('now')
		WHERE id = (SELECT id FROM tasks WHERE status=? ORDER BY id LIMIT 1)
		RETURNING id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, binding_id, binding_version, review_payload,
			workflow_definition_version, workflow_definition_digest, workflow_definition_yaml, park_class, remediation_rounds, created_at, updated_at`,
		workflow.StatusRunning, workflow.StatusQueued)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func scanTask(row *sql.Row) (*workflow.Task, error) {
	var t workflow.Task
	err := row.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.Title, &t.Body,
		&t.Labels, &t.Status, &t.Workflow, &t.Stage, &t.Branch, &t.Plan, &t.Notes,
		&t.PRNumber, &t.TokensUsed, &t.Iterations, &t.Attempt, &t.ParkReason,
		&t.WatchCommentID, &t.RetryCount, &t.Source, &t.Identity,
		&t.BindingID, &t.BindingVersion, &t.ReviewPayload,
		&t.WorkflowDefinitionVersion, &t.WorkflowDefinitionDigest, &t.WorkflowDefinitionYAML,
		&t.ParkClass, &t.RemediationRounds,
		sqliteTime{&t.CreatedAt}, sqliteTime{&t.UpdatedAt})
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ClaimByIssue atomically claims a queued task by owner/repo/issue_number.
// Returns nil if the task is not in queued state (already claimed, parked,
// or terminal). Used by the NATS consumer path where the task was just
// inserted via EnqueueIssue and needs an immediate targeted claim.
func (s *Store) ClaimByIssue(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE tasks SET status=?, attempt=attempt+1, updated_at=datetime('now')
		WHERE owner=? AND repo=? AND issue_number=? AND status=?
		RETURNING id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, binding_id, binding_version, review_payload,
			workflow_definition_version, workflow_definition_digest, workflow_definition_yaml, park_class, remediation_rounds, created_at, updated_at`,
		workflow.StatusRunning, owner, repo, number, workflow.StatusQueued)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// ErrStaleTransition is returned when a guarded transition fails because
// the task's current status does not match the expected from status.

// Transition moves a task to a new status and records the audit detail. The
// from status guards the update; a mismatch returns ErrStaleTransition without
// writing an audit row. Transitioning to parked also stores detail as
// ParkReason in the same transaction. Detail used to exist only in the
// timeline, which left daemon-side parks looking reasonless in task APIs and
// the dashboard.
func (s *Store) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status=?,
			park_reason=CASE WHEN ?=? THEN ? ELSE park_reason END,
			park_class=CASE WHEN ?=? THEN ? ELSE park_class END,
			updated_at=datetime('now')
		WHERE id=? AND status=?`,
		to, to, workflow.StatusParked, clip(detail, 4000),
		to, to, taskstate.ParkNeedsHuman, taskID, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, ?, ?)`,
		taskID, from, to, clip(detail, 4000))
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ParkTask is the classified park write: the same guarded running->parked
// transition Transition performs, carrying the park class the site chose.
// The class is normalized here so an unknown value persists as needs_human
// -- the safe misread is "an operator should look at this".
func (s *Store) ParkTask(ctx context.Context, taskID int64, from, detail, class string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status=?,
			park_reason=?,
			park_class=?,
			updated_at=datetime('now')
		WHERE id=? AND status=?`,
		workflow.StatusParked, clip(detail, 4000),
		taskstate.NormalizeParkClass(class), taskID, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, ?, ?)`,
		taskID, from, workflow.StatusParked, clip(detail, 4000)); err != nil {
		return err
	}

	return tx.Commit()
}

// Update persists mutable task fields written by workflows.
func (s *Store) Update(ctx context.Context, t *workflow.Task) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET workflow=?, stage=?, branch=?, plan=?, notes=?,
			pr_number=?, tokens_used=?, iterations=?, park_reason=?,
			watch_comment_id=?, retry_count=?, remediation_rounds=?, review_payload=?, workflow_definition_version=?,
			workflow_definition_digest=?, workflow_definition_yaml=?, updated_at=datetime('now')
		WHERE id=?`,
		t.Workflow, t.Stage, t.Branch, t.Plan, t.Notes,
		t.PRNumber, t.TokensUsed, t.Iterations, clip(t.ParkReason, 4000),
		t.WatchCommentID, t.RetryCount, t.RemediationRounds, t.ReviewPayload, t.WorkflowDefinitionVersion,
		t.WorkflowDefinitionDigest, t.WorkflowDefinitionYAML, t.ID)
	return err
}

// BeginRemediation transitions an Archie-owned open-PR task into a queued
// remediate run carrying the JSON-encoded review unit. The guarded update is
// the reaction consumer's dedup: a re-delivered reaction, or a second
// consumer racing the first, fails the status guard instead of starting a
// second round. The unit is written in the same transaction, so a task can
// never be claimed into a remediate run whose payload has not landed.
func (s *Store) BeginRemediation(ctx context.Context, taskID int64, payload string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status=?, workflow=?, stage='', park_reason='', park_class='needs_human', review_payload=?,
			updated_at=datetime('now')
		WHERE id=? AND status=?`, workflow.StatusQueued, "remediate", clip(payload, 4000), taskID, workflow.StatusPROpen)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, ?, ?)`,
		taskID, workflow.StatusPROpen, workflow.StatusQueued, "review reaction queued remediation"); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateReviewPayload replaces a queued remediation's review unit: the
// reaction consumer appends late-arriving comments of the same review while
// the unit is still pending. The guarded update is the boundary — once a
// run is claimed, its input is frozen; a late comment is dropped by the
// caller rather than racing the builder's mission.
func (s *Store) UpdateReviewPayload(ctx context.Context, taskID int64, payload string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET review_payload=?, updated_at=datetime('now')
		WHERE id=? AND status=? AND workflow=?`,
		clip(payload, 4000), taskID, workflow.StatusQueued, "remediate")
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}
	return nil
}

// SetReviewCursors persists the poll backstop's per-task high-water marks.
// The pr_open guard keeps a cursor from moving while a remediation run owns
// the task: the run's next round re-reads the cursors the scan wrote, and a
// write under a running task would desync them from what was consumed.
func (s *Store) SetReviewCursors(ctx context.Context, taskID, reviewCursor, commentCursor int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET review_cursor=?, watch_comment_id=?, updated_at=datetime('now')
		WHERE id=? AND status=?`,
		reviewCursor, commentCursor, taskID, workflow.StatusPROpen)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}
	return nil
}

// TaskByIssue returns the task tracking an issue, or nil.
func (s *Store) TaskByIssue(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, binding_id, binding_version, review_payload,
			workflow_definition_version, workflow_definition_digest, workflow_definition_yaml, park_class, remediation_rounds, created_at, updated_at
		FROM tasks WHERE owner=? AND repo=? AND issue_number=?`, owner, repo, number)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// OpenTaskByPR returns the live task that owns the given pull request, or nil.
func (s *Store) OpenTaskByPR(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, binding_id, binding_version, review_payload,
			workflow_definition_version, workflow_definition_digest, workflow_definition_yaml, park_class, remediation_rounds, created_at, updated_at
		FROM tasks
		WHERE owner=? AND repo=? AND pr_number=? AND status=?`,
		owner, repo, number, workflow.StatusPROpen)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TaskByID returns the task with the given database ID, or nil. Used by
// chat controls (/approve, /cancel) that reference a task by its real
// ID rather than a forge issue number.
func (s *Store) TaskByID(ctx context.Context, taskID int64) (*workflow.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, binding_id, binding_version, review_payload,
			workflow_definition_version, workflow_definition_digest, workflow_definition_yaml, park_class, remediation_rounds, created_at, updated_at
		FROM tasks WHERE id=?`, taskID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// Requeue puts a task back on the queue. A non-empty workflow forces it
// (the waiting_human → approved → implement handoff); empty keeps the
// task's current workflow (retrying a parked task).
// The fromStatus acts as a guard: the UPDATE only affects rows whose
// current status matches fromStatus. If no row matches, ErrStaleTransition
// is returned and no audit row is written. Both statements execute in a
// single transaction.
func (s *Store) Requeue(ctx context.Context, taskID int64, fromStatus, wf string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status=?,
			workflow=CASE WHEN ?='' THEN workflow ELSE ? END,
			stage='', park_reason='', park_class='needs_human', updated_at=datetime('now')
		WHERE id=? AND status=?`, workflow.StatusQueued, wf, wf, taskID, fromStatus)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, ?, ?)`,
		taskID, fromStatus, workflow.StatusQueued, "requeued "+wf)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// RetryTask requeues a task and increments retry_count in the same guarded
// transaction. A queued task with an uncounted retry would make max_retries a
// suggestion rather than a cap.
func (s *Store) RetryTask(ctx context.Context, taskID int64, fromStatus, wf string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status=?, retry_count=retry_count+1,
			workflow=CASE WHEN ?='' THEN workflow ELSE ? END,
			stage='', park_reason='', park_class='needs_human', updated_at=datetime('now')
		WHERE id=? AND status=?`, workflow.StatusQueued, wf, wf, taskID, fromStatus)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleTransition
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, ?, ?)`,
		taskID, fromStatus, workflow.StatusQueued, "retried "+wf); err != nil {
		return err
	}
	return tx.Commit()
}

// ArchiveTask removes one terminal task from the active task board. The
// expected status is part of the delete predicate so a stale browser cannot
// remove a task that has since resumed or otherwise changed state. Historical
// transition and activity rows remain as the operator audit trail.
func (s *Store) ArchiveTask(
	ctx context.Context,
	taskID int64,
	fromStatus string,
	audit events.Event,
) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	eventID, err := insertEvent(ctx, tx, audit)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=? AND status=?`, taskID, fromStatus)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrStaleTransition
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return eventID, nil
}

// RecoverStale re-queues tasks left running by a crashed daemon.
func (s *Store) RecoverStale(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status=?, updated_at=datetime('now') WHERE status=?`,
		workflow.StatusQueued, workflow.StatusRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// OpenPRs returns tasks whose PR state should be reconciled with GitHub.
//
// It carries attempt deliberately: the reconcile loop attributes pr_merged and
// pr_rejected to the attempt that opened the PR, and it reaches the state store
// over the wire, so the row must carry the value for taskProto to publish it.
// Narrowing this projection back silently sends those events out unattributed.
func (s *Store) OpenPRs(ctx context.Context) (tasks []workflow.Task, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, repo, issue_number, pr_number, status, source, identity, attempt,
			review_cursor, watch_comment_id
		FROM tasks WHERE status=?`, workflow.StatusPROpen)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var t workflow.Task
		if err := rows.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.PRNumber,
			&t.Status, &t.Source, &t.Identity, &t.Attempt,
			&t.ReviewCursor, &t.WatchCommentID); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ClearTerminalTasks deletes tasks whose status is terminal. Parked work is
// deliberately excluded because it is recoverable.
func (s *Store) ClearTerminalTasks(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tasks WHERE status IN (?, ?, ?, ?)`,
		workflow.StatusMerged, workflow.StatusRejected, workflow.StatusDead, workflow.StatusClosedWontDo)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Walk forward by runes; stop when the next rune would exceed n bytes.
	pos := 0
	for pos < len(s) {
		_, size := utf8.DecodeRuneInString(s[pos:])
		if pos+size > n {
			break
		}
		pos += size
	}
	return s[:pos]
}

// sqliteTime scans SQLite's TEXT datetime columns into a time.Time.
//
// The tasks table stores timestamps as TEXT via datetime('now') rather than a
// typed column, so the driver hands back a string and the standard time.Time
// scan fails. Parsing here keeps the column definition untouched -- changing
// it would need a migration over existing rows for no behavioural gain.
type sqliteTime struct{ t *time.Time }

// sqliteTimeLayout is what datetime('now') produces: UTC, no zone suffix.
const sqliteTimeLayout = "2006-01-02 15:04:05"

func (s sqliteTime) Scan(v any) error {
	switch value := v.(type) {
	case nil:
		return nil
	case time.Time:
		*s.t = value.UTC()
		return nil
	case []byte:
		return s.parse(string(value))
	case string:
		return s.parse(value)
	default:
		return fmt.Errorf("store: cannot scan %T into time", v)
	}
}

func (s sqliteTime) parse(v string) error {
	if v == "" {
		return nil
	}
	// RFC3339 first: rows written by other paths may carry a zone.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, sqliteTimeLayout} {
		if parsed, err := time.Parse(layout, v); err == nil {
			*s.t = parsed.UTC()
			return nil
		}
	}
	return fmt.Errorf("store: unrecognised time %q", v)
}
