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

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Task lifecycle statuses. Workflows move tasks between them; the
// daemon owns queued→running claims and crash recovery.
const (
	// Defined in internal/taskstate so the dashboard, chat and the store
	// share one vocabulary. These names are kept as the store's public
	// spelling; the values live in one place.
	StatusQueued       = taskstate.Queued
	StatusRunning      = taskstate.Running
	StatusWaitingHuman = taskstate.WaitingHuman
	StatusPROpen       = taskstate.PROpen
	StatusMerged       = taskstate.Merged
	StatusParked       = taskstate.Parked
	StatusDead         = taskstate.Dead
	StatusRejected     = taskstate.Rejected
	StatusClosedWontDo = taskstate.Declined
)

type Task struct {
	ID          int64  `json:"id"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issue_number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Labels      string `json:"labels"` // comma-separated, as seen at enqueue time
	Status      string `json:"status"`
	Workflow    string `json:"workflow"`
	Stage       string `json:"stage"`
	Branch      string `json:"branch"`
	Plan        string `json:"plan"`
	Notes       string `json:"notes"`
	PRNumber    int    `json:"pr_number"`
	TokensUsed  int    `json:"tokens_used"`
	Iterations  int    `json:"iterations"`
	Attempt     int    `json:"attempt"`
	ParkReason  string `json:"park_reason"`
	// RetryCount tracks how many times a parked task has been retried
	// (parking-to-queued transitions). When it reaches the configured
	// max_retries the daemon moves the task to StatusDead.
	RetryCount int `json:"retry_count"`
	// WatchCommentID: replies to the issue after this comment are the
	// human input a waiting_human task is blocked on.
	WatchCommentID int64 `json:"watch_comment_id"`
	// Source is "forge" (default; a real forge issue backs this task) or
	// "chat" (created via /spawn with no forge issue). Workflow stages
	// and daemon reconciliation must skip forge-only operations (issue
	// labels, comments, replies) for chat-sourced tasks; IssueNumber on
	// a chat task is a synthetic value, not a real forge issue.
	Source string `json:"source"`
	// Identity is the archie identity that owns this task (chat-spawned
	// tasks only; empty for forge-sourced tasks and single-identity
	// deployments). Used to scope /approve and /cancel authorization so
	// one identity cannot control another's chat-spawned tasks.
	Identity string `json:"identity"`
	// CreatedAt and UpdatedAt are the SQLite row timestamps, exposed so
	// callers can show a task's age and last activity. They are written by
	// column defaults and the UPDATE statements, never by the caller.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SourceForge and SourceChat are the valid values for Task.Source.
// Legacy rows and forge-polled tasks have Source == SourceForge (the
// zero value defaults there via the SQLite column default).
const (
	SourceForge = "forge"
	SourceChat  = "chat"
)

// IsForgeBacked reports whether t corresponds to a real forge issue.
// Chat-spawned tasks (Source == SourceChat) have a synthetic
// IssueNumber and must not be used in forge label/comment/reply calls.
func (t Task) IsForgeBacked() bool {
	return t.Source != SourceChat
}

type Store struct{ db *sql.DB }

const taskSchemaVersion = 1

// Open opens (creating if needed) the SQLite database and its schema.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema+eventsSchema+capturesSchema); err != nil {
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
	if version == taskSchemaVersion {
		return tx.Commit()
	}

	columns, err := taskColumns(ctx, tx)
	if err != nil {
		return err
	}

	migrations := []struct {
		column string
		sql    string
	}{
		{"watch_comment_id", `ALTER TABLE tasks ADD COLUMN watch_comment_id INTEGER NOT NULL DEFAULT 0`},
		{"retry_count", `ALTER TABLE tasks ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`},
		{"source", `ALTER TABLE tasks ADD COLUMN source TEXT NOT NULL DEFAULT 'forge'`},
		{"identity", `ALTER TABLE tasks ADD COLUMN identity TEXT NOT NULL DEFAULT ''`},
	}
	for _, migration := range migrations {
		if columns[migration.column] {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

func taskColumns(ctx context.Context, tx *sql.Tx) (_ map[string]bool, retErr error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(tasks)`)
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
	status        TEXT NOT NULL DEFAULT 'queued',
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
	retry_count   INTEGER NOT NULL DEFAULT 0,
	source        TEXT NOT NULL DEFAULT 'forge',
	identity      TEXT NOT NULL DEFAULT '',
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

// IncrementRetryCount atomically bumps retry_count by 1. Called when an
// operator retries a parked task; the caller enforces max_retries against
// the resulting count.
func (s *Store) IncrementRetryCount(ctx context.Context, taskID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET retry_count = retry_count + 1, updated_at = datetime('now') WHERE id = ?`, taskID)
	return err
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
// Workflow stages and daemon reconciliation must check Task.IsForgeBacked()
// before treating it as a real forge issue number.
func (s *Store) EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string) (*Task, error) {
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
			source, identity, created_at, updated_at`,
		owner, repo, owner, repo, syntheticIssueNumberBase-1, title, body, workflow, identity)
	return scanTask(row)
}

// ClaimNext atomically moves the oldest queued task to running and
// returns it; nil when the queue is empty.
func (s *Store) ClaimNext(ctx context.Context) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE tasks SET status='running', attempt=attempt+1, updated_at=datetime('now')
		WHERE id = (SELECT id FROM tasks WHERE status='queued' ORDER BY id LIMIT 1)
		RETURNING id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, created_at, updated_at`)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func scanTask(row *sql.Row) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.Title, &t.Body,
		&t.Labels, &t.Status, &t.Workflow, &t.Stage, &t.Branch, &t.Plan, &t.Notes,
		&t.PRNumber, &t.TokensUsed, &t.Iterations, &t.Attempt, &t.ParkReason,
		&t.WatchCommentID, &t.RetryCount, &t.Source, &t.Identity,
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
func (s *Store) ClaimByIssue(ctx context.Context, owner, repo string, number int) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE tasks SET status='running', attempt=attempt+1, updated_at=datetime('now')
		WHERE owner=? AND repo=? AND issue_number=? AND status='queued'
		RETURNING id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, created_at, updated_at`, owner, repo, number)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// ErrStaleTransition is returned when a guarded transition fails because
// the task's current status does not match the expected from status.
var ErrStaleTransition = errors.New("store: stale transition: task status does not match expected from status")

// Transition moves a task to a new status, recording the change.
// The from status acts as a guard: the UPDATE only affects rows whose
// current status matches from. If no row matches, ErrStaleTransition is
// returned and no audit row is written. Both statements execute in a
// single transaction.
func (s *Store) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status=?, updated_at=datetime('now') WHERE id=? AND status=?`, to, taskID, from)
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

// Update persists mutable task fields written by workflows.
func (s *Store) Update(ctx context.Context, t *Task) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET workflow=?, stage=?, branch=?, plan=?, notes=?,
			pr_number=?, tokens_used=?, iterations=?, park_reason=?,
			watch_comment_id=?, updated_at=datetime('now')
		WHERE id=?`,
		t.Workflow, t.Stage, t.Branch, t.Plan, t.Notes,
		t.PRNumber, t.TokensUsed, t.Iterations, clip(t.ParkReason, 4000),
		t.WatchCommentID, t.ID)
	return err
}

// TaskByIssue returns the task tracking an issue, or nil.
func (s *Store) TaskByIssue(ctx context.Context, owner, repo string, number int) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, created_at, updated_at
		FROM tasks WHERE owner=? AND repo=? AND issue_number=?`, owner, repo, number)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TaskByID returns the task with the given database ID, or nil. Used by
// chat controls (/approve, /cancel) that reference a task by its real
// ID rather than a forge issue number.
func (s *Store) TaskByID(ctx context.Context, taskID int64) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason, watch_comment_id, retry_count,
			source, identity, created_at, updated_at
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
func (s *Store) Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status='queued',
			workflow=CASE WHEN ?='' THEN workflow ELSE ? END,
			stage='', park_reason='', updated_at=datetime('now')
		WHERE id=? AND status=?`, workflow, workflow, taskID, fromStatus)
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
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, 'queued', ?)`,
		taskID, fromStatus, "requeued "+workflow)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// RetryTask requeues a task and increments retry_count in the same guarded
// transaction. A queued task with an uncounted retry would make max_retries a
// suggestion rather than a cap.
func (s *Store) RetryTask(ctx context.Context, taskID int64, fromStatus, workflow string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status='queued', retry_count=retry_count+1,
			workflow=CASE WHEN ?='' THEN workflow ELSE ? END,
			stage='', park_reason='', updated_at=datetime('now')
		WHERE id=? AND status=?`, workflow, workflow, taskID, fromStatus)
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
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, 'queued', ?)`,
		taskID, fromStatus, "retried "+workflow); err != nil {
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
		`UPDATE tasks SET status='queued', updated_at=datetime('now') WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// OpenPRs returns tasks whose PR state should be reconciled with GitHub.
func (s *Store) OpenPRs(ctx context.Context) (tasks []Task, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, repo, issue_number, pr_number, status, source, identity
		FROM tasks WHERE status='pr_open'`)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.PRNumber,
			&t.Status, &t.Source, &t.Identity); err != nil {
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
		`DELETE FROM tasks WHERE status IN ('merged','rejected','dead','closed_wont_do')`)
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
