package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// syntheticIssueBase mirrors the store's chat issue-number seed. It is the
// fallback for a repo's first chat task, not the number that lands: the
// allocator adds one to it.
const syntheticIssueBase = 1_000_000_000_000_000

// migrated is a pool with the State Store schema applied, and its queries.
func migrated(t *testing.T) (*pgxpool.Pool, *postgresdb.Queries) {
	t.Helper()
	pool, err := Open(t.Context(), pgtest.URL(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(t.Context(), pool, Migrations()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pool, postgresdb.New(pool)
}

// queriesFor is migrated's queries without the pool, for tests that only read
// and write through sqlc.
func queriesFor(t *testing.T) *postgresdb.Queries {
	t.Helper()
	_, q := migrated(t)
	return q
}

// insertChatTask queues one chat-sourced task.
func insertChatTask(t *testing.T, q *postgresdb.Queries) postgresdb.Task {
	t.Helper()
	task, err := q.InsertChatTask(t.Context(), postgresdb.InsertChatTaskParams{
		Owner: "acme", Repo: "widget", Title: "t", Body: "b",
		Workflow: "tdd", Identity: "bot",
		FallbackIssueNumber: syntheticIssueBase,
	})
	if err != nil {
		t.Fatalf("InsertChatTask: %v", err)
	}
	return task
}

func TestSchemaAppliesAndTasksRoundTrip(t *testing.T) {
	q := queriesFor(t)

	task := insertChatTask(t, q)
	if task.Status != "queued" {
		t.Fatalf("new task status = %q, want queued", task.Status)
	}
	if task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at were not populated")
	}
	if task.IssueNumber != syntheticIssueBase+1 {
		t.Fatalf("first chat task issue number = %d, want %d", task.IssueNumber, syntheticIssueBase+1)
	}

	got, err := q.TaskByIssue(t.Context(), postgresdb.TaskByIssueParams{
		Owner: task.Owner, Repo: task.Repo, IssueNumber: task.IssueNumber,
	})
	if err != nil {
		t.Fatalf("TaskByIssue: %v", err)
	}
	if got.ID != task.ID {
		t.Fatalf("TaskByIssue = %d, want %d", got.ID, task.ID)
	}
}

// The chat allocator hands out the next number per repo, so two chat tasks in
// one repo never share an issue number.
func TestChatIssueNumberAllocatorIsPerRepo(t *testing.T) {
	q := queriesFor(t)

	first := insertChatTask(t, q)
	second := insertChatTask(t, q)
	if second.IssueNumber != first.IssueNumber+1 {
		t.Fatalf("second chat issue number = %d, want %d", second.IssueNumber, first.IssueNumber+1)
	}
}

// The issue-number uniqueness the SQLite schema carried as a table constraint
// survives the move.
func TestTaskIssueNumberIsUnique(t *testing.T) {
	pool, q := migrated(t)
	task := insertChatTask(t, q)

	_, err := pool.Exec(t.Context(), `
		INSERT INTO tasks (owner, repo, issue_number) VALUES ($1, $2, $3)`,
		task.Owner, task.Repo, task.IssueNumber)
	if err == nil {
		t.Fatal("a second task for the same owner/repo/issue was accepted, want the unique constraint refused it")
	}
}

// Claiming takes the oldest queued task, in id order, and counts the attempt.
func TestClaimNextTakesTheOldestAndCountsTheAttempt(t *testing.T) {
	q := queriesFor(t)
	first := insertChatTask(t, q)
	second := insertChatTask(t, q)

	claimed, err := q.ClaimNextTask(t.Context())
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if claimed.ID != first.ID || claimed.Status != "running" || claimed.Attempt != 1 {
		t.Fatalf("first claim = {id:%d status:%q attempt:%d}, want {id:%d running 1}", claimed.ID, claimed.Status, claimed.Attempt, first.ID)
	}

	claimed, err = q.ClaimNextTask(t.Context())
	if err != nil {
		t.Fatalf("second ClaimNextTask: %v", err)
	}
	if claimed.ID != second.ID {
		t.Fatalf("second claim = %d, want %d", claimed.ID, second.ID)
	}

	if _, err := q.ClaimNextTask(t.Context()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on an empty queue = %v, want pgx.ErrNoRows", err)
	}
}

// A claimer must not wait behind a row another transaction holds: it takes the
// next one. This is what FOR UPDATE SKIP LOCKED buys over the SQLite version,
// where a second claimer could only ever have blocked on the writer lock.
func TestClaimSkipsARowAnotherTransactionHolds(t *testing.T) {
	pool, q := migrated(t)
	held := insertChatTask(t, q)
	free := insertChatTask(t, q)

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	var lockedID int64
	if err := tx.QueryRow(t.Context(),
		`SELECT id FROM tasks WHERE status = 'queued' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`,
	).Scan(&lockedID); err != nil {
		t.Fatalf("lock the oldest queued row: %v", err)
	}
	if lockedID != held.ID {
		t.Fatalf("locked row %d, want the oldest queued row %d", lockedID, held.ID)
	}

	claimed, err := q.ClaimNextTask(t.Context())
	if err != nil {
		t.Fatalf("ClaimNextTask while a row is held: %v", err)
	}
	if claimed.ID != free.ID {
		t.Fatalf("claimed %d while %d was held, want the skipped-to row %d", claimed.ID, free.ID, held.ID)
	}
}
