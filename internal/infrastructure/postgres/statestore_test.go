package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
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

func resourcesFor(t *testing.T) *Resources {
	t.Helper()
	pool, _ := migrated(t)
	return NewResources(pool)
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

// A control-plane resource write is optimistic and idempotent: a retry returns
// the audited original, while a different request based on a stale revision is
// refused. The history remains the audit trail, newest first.
func TestResourceWritesPreserveOptimisticAndIdempotentSemantics(t *testing.T) {
	resources := resourcesFor(t)
	firstAt := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	first, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: "settings", Value: []byte(`{"v":1}`), Actor: "operator", Source: "archie-ui",
		RequestID: "r1", ExpectedVersion: 0, At: firstAt,
	})
	if err != nil {
		t.Fatalf("first PutResource: %v", err)
	}
	if first.Version != 1 || first.CurrentVersion != 0 {
		t.Fatalf("first write = %+v, want version 1 from version 0", first)
	}

	replay, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: "settings", Value: []byte(`{"v":999}`), Actor: "other", Source: "retry",
		RequestID: "r1", ExpectedVersion: 0, At: firstAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replay PutResource: %v", err)
	}
	if replay.Version != first.Version || string(replay.Value) != string(first.Value) || replay.Actor != first.Actor {
		t.Fatalf("replay = %+v, want original %+v", replay, first)
	}

	if _, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: "settings", Value: []byte(`{"v":2}`), Actor: "operator", Source: "archie-ui",
		RequestID: "r2", ExpectedVersion: 0, At: firstAt.Add(2 * time.Minute),
	}); !errors.Is(err, storecontract.ErrResourceVersionConflict) {
		t.Fatalf("stale PutResource = %v, want ErrResourceVersionConflict", err)
	}

	second, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: "settings", Value: []byte(`{"v":2}`), Actor: "operator", Source: "messaging",
		RequestID: "r2", ExpectedVersion: 1, At: firstAt.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("second PutResource: %v", err)
	}
	if second.Version != 2 || second.CurrentVersion != 1 {
		t.Fatalf("second write = %+v, want version 2 from version 1", second)
	}
	lateReplay, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: "settings", Value: []byte(`{"v":999}`), Actor: "other", Source: "retry",
		RequestID: "r1", ExpectedVersion: 0, At: firstAt.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatalf("late replay PutResource: %v", err)
	}
	if lateReplay.Version != first.Version || string(lateReplay.Value) != string(first.Value) {
		t.Fatalf("late replay = %+v, want original %+v", lateReplay, first)
	}
	live, err := resources.Resource(t.Context(), "settings")
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if live.Version != second.Version || string(live.Value) != string(second.Value) {
		t.Fatalf("live resource after replay = %+v, want second write %+v", live, second)
	}

	history, err := resources.ResourceHistory(t.Context(), "settings", 1)
	if err != nil {
		t.Fatalf("ResourceHistory: %v", err)
	}
	if len(history) != 1 || history[0].Version != 2 || history[0].Source != "messaging" {
		t.Fatalf("limited history = %+v, want newest revision only", history)
	}
	history, err = resources.ResourceHistory(t.Context(), "settings", 0)
	if err != nil {
		t.Fatalf("unlimited ResourceHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history after late replay = %+v, want two revisions", history)
	}
	if _, err := resources.Resource(t.Context(), "missing"); !errors.Is(err, storecontract.ErrResourceNotFound) {
		t.Fatalf("missing Resource = %v, want ErrResourceNotFound", err)
	}
}

// PostgreSQL admits concurrent writers, unlike SQLite's single writer. The
// resource lock keeps the expected-version comparison and audit append one
// serialized operation.
func TestConcurrentResourceWritesAcceptOnlyOneExpectedVersion(t *testing.T) {
	resources := resourcesFor(t)
	type result struct{ err error }
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, requestID := range []string{"first", "second"} {
		go func() {
			<-start
			_, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
				Kind: "settings", Value: []byte(`{"v":1}`), Actor: "operator", Source: "test",
				RequestID: requestID, ExpectedVersion: 0,
			})
			results <- result{err: err}
		}()
	}
	close(start)

	var succeeded, conflicted int
	for range 2 {
		outcome := <-results
		switch {
		case outcome.err == nil:
			succeeded++
		case errors.Is(outcome.err, storecontract.ErrResourceVersionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent PutResource: %v", outcome.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent writes = %d succeeded, %d conflicted; want 1 each", succeeded, conflicted)
	}

	history, err := resources.ResourceHistory(t.Context(), "settings", 0)
	if err != nil {
		t.Fatalf("ResourceHistory: %v", err)
	}
	if len(history) != 1 || history[0].Version != 1 {
		t.Fatalf("history after race = %+v, want exactly one version 1", history)
	}
}

// TestResourceWritesRecordFieldLevelAudit: each write records one sys_audit row
// per field it changed, newest first, and a replayed write records nothing.
func TestResourceWritesRecordFieldLevelAudit(t *testing.T) {
	resources := resourcesFor(t)
	put := func(value string, expected int64, request string) {
		t.Helper()
		if _, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
			Kind: "settings", Value: []byte(value), Actor: "sam", Source: "archie-ui",
			RequestID: request, ExpectedVersion: expected,
		}); err != nil {
			t.Fatalf("PutResource(%s): %v", value, err)
		}
	}
	put(`{"a":1,"b":2}`, 0, "r1")
	put(`{"a":1,"b":3,"c":4}`, 1, "r2")
	put(`{"a":1,"b":3,"c":4}`, 1, "r2")

	audit, err := resources.Audit(t.Context(), storecontract.AuditTableResources, []string{"settings"}, 0)
	if err != nil {
		t.Fatalf("ResourceAudit: %v", err)
	}
	got := make([]string, 0, len(audit))
	for _, entry := range audit {
		got = append(got, fmt.Sprintf("v%d %s %s->%s by %s", entry.Version, entry.Field, entry.OldValue, entry.NewValue, entry.Actor))
	}
	want := []string{
		"v2 c ->4 by sam", "v2 b 2->3 by sam",
		"v1 b ->2 by sam", "v1 a ->1 by sam",
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("audit =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
