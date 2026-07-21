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

	_ "modernc.org/sqlite"
)

// Task lifecycle statuses. Workflows move tasks between them; the
// daemon owns queued→running claims and crash recovery.
const (
	StatusQueued       = "queued"
	StatusRunning      = "running"
	StatusWaitingHuman = "waiting_human"
	StatusPROpen       = "pr_open"
	StatusMerged       = "merged"
	StatusParked       = "parked"
	StatusRejected     = "rejected"
	StatusClosedWontDo = "closed_wont_do"
)

type Task struct {
	ID          int64
	Owner       string
	Repo        string
	IssueNumber int
	Title       string
	Body        string
	Labels      string // comma-separated, as seen at enqueue time
	Status      string
	Workflow    string
	Stage       string
	Branch      string
	Plan        string
	Notes       string
	PRNumber    int
	TokensUsed  int
	Iterations  int
	Attempt     int
	ParkReason  string
	// WatchCommentID: replies to the issue after this comment are the
	// human input a waiting_human task is blocked on.
	WatchCommentID int64
}

type Store struct{ db *sql.DB }

// Open opens (creating if needed) the SQLite database and its schema.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema + eventsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: init schema: %w", err)
	}
	// Additive migrations; "duplicate column" means already applied.
	if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN watch_comment_id INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
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

// EnqueueIssue inserts a new queued task for the issue; returns false if
// the issue is already tracked (the idempotency key is owner/repo/number).
func (s *Store) EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (owner, repo, issue_number, title, body, labels)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner, repo, issue_number) DO NOTHING`,
		owner, repo, number, title, body, labels)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClaimNext atomically moves the oldest queued task to running and
// returns it; nil when the queue is empty.
func (s *Store) ClaimNext(ctx context.Context) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE tasks SET status='running', attempt=attempt+1, updated_at=datetime('now')
		WHERE id = (SELECT id FROM tasks WHERE status='queued' ORDER BY id LIMIT 1)
		RETURNING id, owner, repo, issue_number, title, body, labels, status,
			workflow, stage, branch, plan, notes, pr_number, tokens_used,
			iterations, attempt, park_reason`)
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
		&t.PRNumber, &t.TokensUsed, &t.Iterations, &t.Attempt, &t.ParkReason)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Transition moves a task to a new status, recording the change.
func (s *Store) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status=?, updated_at=datetime('now') WHERE id=?`, to, taskID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, ?, ?)`,
		taskID, from, to, clip(detail, 4000))
	return err
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
			iterations, attempt, park_reason
		FROM tasks WHERE owner=? AND repo=? AND issue_number=?`, owner, repo, number)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// WaitingTasks returns tasks blocked on human input.
func (s *Store) WaitingTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, repo, issue_number, title, body, plan, workflow,
			watch_comment_id, status
		FROM tasks WHERE status='waiting_human'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.Title,
			&t.Body, &t.Plan, &t.Workflow, &t.WatchCommentID, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Requeue puts a task back on the queue. A non-empty workflow forces it
// (the waiting_human → approved → implement handoff); empty keeps the
// task's current workflow (retrying a parked task).
func (s *Store) Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET status='queued',
			workflow=CASE WHEN ?='' THEN workflow ELSE ? END,
			stage='', park_reason='', updated_at=datetime('now')
		WHERE id=?`, workflow, workflow, taskID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO transitions (task_id, from_status, to_status, detail) VALUES (?, ?, 'queued', ?)`,
		taskID, fromStatus, "requeued "+workflow)
	return err
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
func (s *Store) OpenPRs(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, repo, issue_number, pr_number, status FROM tasks WHERE status='pr_open'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.PRNumber, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
