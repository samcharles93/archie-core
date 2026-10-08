package postgres_test

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// StartCall is idempotent per call key: the same caller and key return the
// child already started, so a retry of a lost EnqueueCallTask reply, or of the
// stage that made the call, does not start a second child and run its work
// twice. Another call site -- the same declared path starting a different
// workflow -- still gets its own child.
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

	first, err := db.StartCall(ctx, caller.ID, "check@callee-a", "callee-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := db.StartCall(ctx, caller.ID, "check@callee-a", "callee-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID {
		t.Fatalf("the retry started task %d, want the child already started, %d", retry.ID, first.ID)
	}
	other, err := db.StartCall(ctx, caller.ID, "check@callee-b", "callee-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatalf("the same path starting another workflow returned the first child, %d", first.ID)
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

// A child that reached a hard terminal failure can never do the work, so it
// frees its key: it left no result the caller can use, and the caller's retry
// starting a fresh child duplicates nothing. A parked child keeps the key --
// it is recoverable by retrying the child, which is what the caller's own park
// names.
func TestStartCallFreesTheKeyOfAHardTerminalChild(t *testing.T) {
	tests := []struct {
		name string
		// move takes the child out of queued the way its real ending does.
		move    func(t *testing.T, db *pgstore.TaskDB, childID int64)
		wantNew bool
	}{
		{
			name: "a declined child frees its key",
			move: func(t *testing.T, db *pgstore.TaskDB, childID int64) {
				t.Helper()
				if err := db.Transition(t.Context(), childID, taskstate.Queued, taskstate.Declined, "the operator declined it"); err != nil {
					t.Fatal(err)
				}
			},
			wantNew: true,
		},
		{
			name: "a parked child keeps its key",
			move: func(t *testing.T, db *pgstore.TaskDB, childID int64) {
				t.Helper()
				// Parked is reachable only from running, so the child is
				// claimed first, as a worker would claim it.
				claimed, err := db.ClaimNext(t.Context())
				if err != nil || claimed == nil || claimed.ID != childID {
					t.Fatalf("claim child %d: %v, got %v", childID, err, claimed)
				}
				if err := db.ParkTask(t.Context(), childID, taskstate.Running, "needs an operator", taskstate.ParkNeedsHuman); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			db := pgstore.Open(t)
			if _, err := db.EnqueueChatTask(ctx, "", "", "caller", "", "tdd", "", "", nil); err != nil {
				t.Fatal(err)
			}
			caller, err := db.ClaimNext(ctx)
			if err != nil || caller == nil {
				t.Fatalf("claim: %v", err)
			}
			first, err := db.StartCall(ctx, caller.ID, "check@callee", "callee", nil)
			if err != nil {
				t.Fatal(err)
			}
			tt.move(t, db, first.ID)

			retry, err := db.StartCall(ctx, caller.ID, "check@callee", "callee", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNew && retry.ID == first.ID {
				t.Fatalf("the retry returned the child that can never do the work, %d, want a fresh child", first.ID)
			}
			if !tt.wantNew && retry.ID != first.ID {
				t.Fatalf("the retry started task %d, want the parked child it can retry, %d", retry.ID, first.ID)
			}
		})
	}
}
