package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestClaimTurnConcurrentInsertRaceNeverReturnsAnError guards against a
// variable-shadowing bug in ClaimTurn (session_store_sqlite.go), fixed
// alongside this test: the branch handling "another connection inserted the
// same turn between our read and insert" used to write its retry read's
// result into an `err` shadowed inside the `errors.Is(err, sql.ErrNoRows)`
// block by an earlier `result, err := tx.ExecContext(...)`. That shadowed
// err went out of scope at the block's closing brace, so the outer err
// checked afterward was still sql.ErrNoRows from the original read -- the
// loser of that race would always return an error wrapped
// "sessionstore: read turn: %w", even though the comment's own stated
// intent is "apply the normal claim state machine instead of surfacing a
// uniqueness error." Confirmed via careful reading of Go's block-scoping
// rules, not assumed.
//
// Honest limitation: this test could NOT be made to reproduce that exact
// branch through genuine concurrent access. A one-off diagnostic (real
// two-connection race, sleep-widened to force overlap, reverted before this
// commit -- never present in a shipped file) showed the loser instead fails
// its own INSERT outright with SQLITE_BUSY_SNAPSHOT under WAL mode with this
// driver, before ON CONFLICT DO NOTHING can ever report affected=0. That
// means the specific "0 rows affected" branch this bug lived in may be
// effectively unreachable via a same-driver, same-journal-mode race today.
// The fix stands regardless -- the shadowing was real, wrong on its own
// terms, and free to correct -- but this test cannot claim to exercise the
// exact line that was fixed. What it verifies is the weaker, still real
// property that matters operationally: many genuinely concurrent
// same-turn-ID claims across separate connections never produce the bug's
// specific error signature. Treat this as a guard against a regression
// becoming reachable (a different SQLite version, journal mode, or driver
// with weaker snapshot isolation), not as proof the original bug was ever
// live in production.
//
// A single sqliteSessionStore instance cannot exhibit any of this: its own
// mutex serializes every ClaimTurn call against that instance's connection,
// so this uses two separate store instances (two connections, two mutexes)
// against the same file-backed database -- the real-world shape of the
// scenario (two archied/worker processes or connections sharing one session
// DB).
//
// SQLite's own write-lock contention (SQLITE_BUSY / SQLITE_BUSY_SNAPSHOT,
// "database is locked") is infrastructure noise distinct from the bug: it
// comes from BeginTx/ExecContext/Commit failing outright and is wrapped
// with a different prefix ("claim turn: begin/insert/rows affected/commit").
// Only the "sessionstore: read turn:" wrap was the bug's unique signature,
// so transient busy errors are retried rather than treated as failures.
func TestClaimTurnConcurrentInsertRaceNeverReturnsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")

	const stores = 2
	const trials = 100
	const maxRetriesPerAttempt = 20

	openers := make([]TurnLedger, stores)
	for i := range openers {
		store, err := OpenSQLiteSessionStore(path)
		if err != nil {
			t.Fatalf("OpenSQLiteSessionStore[%d]: %v", i, err)
		}
		closer, ok := store.(interface{ Close() error })
		if !ok {
			t.Fatalf("store[%d] is %T, want a Close() error", i, store)
		}
		t.Cleanup(func() { _ = closer.Close() })
		ledger, ok := store.(TurnLedger)
		if !ok {
			t.Fatalf("store[%d] is %T, want TurnLedger", i, store)
		}
		openers[i] = ledger
	}

	ctx := context.Background()
	now := time.UnixMilli(1000).UTC()

	claimRetryingBusy := func(ledger TurnLedger, initial TurnRecord) error {
		var err error
		for range maxRetriesPerAttempt {
			_, _, err = ledger.ClaimTurn(ctx, initial)
			if err == nil || !strings.Contains(err.Error(), "database is locked") {
				return err
			}
		}
		return err
	}

	// Each trial races the two independent stores over one turn ID, then waits
	// before starting the next trial. Running every trial at once adds 100-way
	// global writer contention that is unrelated to the same-turn insert race
	// this regression covers and can exhaust the bounded SQLITE_BUSY retries
	// before any competing transaction gets scheduled to finish.
	for trial := range trials {
		turnID := fmt.Sprintf("race-turn-%d", trial)
		initial := TurnRecord{
			TurnID:    turnID,
			SessionID: "race-session",
			SourceID:  turnID,
			Status:    TurnStatusAccepted,
			CreatedAt: now,
			UpdatedAt: now,
		}
		errs := make([]error, stores)
		var wg sync.WaitGroup
		var start sync.WaitGroup
		start.Add(1)
		for i := range stores {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				start.Wait()
				errs[i] = claimRetryingBusy(openers[i], initial)
			}(i)
		}
		start.Done()
		wg.Wait()

		for i, err := range errs {
			if err == nil {
				continue
			}
			if strings.Contains(err.Error(), "sessionstore: read turn:") {
				t.Fatalf("trial %d, store %d: ClaimTurn() error = %v -- a losing racer surfaced the winner's insert as a read failure instead of applying the normal claim state machine", trial, i, err)
			}
			t.Fatalf("trial %d, store %d: ClaimTurn() unexpected error = %v", trial, i, err)
		}
	}
}
