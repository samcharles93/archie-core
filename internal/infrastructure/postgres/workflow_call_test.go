package postgres_test

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// StartCall is idempotent per call site: the same caller and call key return
// the child already started, so a retry of a lost EnqueueCallTask reply, or of
// the stage that made the call, does not start a second child and run its work
// twice. A different call site in the same run still gets its own child.
func TestStartCallReturnsOneChildPerCallKey(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	if _, err := db.EnqueueChatTask(ctx, "", "", "caller", "", "tdd", "", "", nil); err != nil {
		t.Fatal(err)
	}
	caller, err := db.ClaimNext(ctx)
	if err != nil || caller == nil {
		t.Fatalf("claim: %v", err)
	}

	first, err := db.StartCall(ctx, caller.ID, "check", "callee", nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := db.StartCall(ctx, caller.ID, "check", "callee", nil)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID {
		t.Fatalf("the retry started task %d, want the child already started, %d", retry.ID, first.ID)
	}
	other, err := db.StartCall(ctx, caller.ID, "verify", "callee", nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatalf("a different call key returned the first child, %d", first.ID)
	}

	var rows int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM tasks WHERE call_parent_task_id = $1`, caller.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("the caller started %d children, want one per call key (2)", rows)
	}
}
