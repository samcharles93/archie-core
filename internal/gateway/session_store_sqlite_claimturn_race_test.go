package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestClaimTurnConcurrentInsertRaceResolvesToOneOwner verifies that ordinary
// contention between processes sharing the session database is resolved by
// ClaimTurn itself. Callers must not need to recognize and retry SQLite's
// SQLITE_BUSY or SQLITE_BUSY_SNAPSHOT implementation details.
//
// Two invariants must hold under contention: every ClaimTurn call returns
// without error, and EXACTLY ONE of the racers ends up TurnClaimOwned while
// the other observes the committed row and returns a non-owning claim.
// The second invariant matters more than the first: a bug that returns
// TurnClaimOwned to both racers means two archied processes drive the same
// turn to completion, and the assistant reply gets emitted twice to the
// chat. The first invariant alone would not catch that.
//
// A single sqliteSessionStore instance cannot exhibit any of this: its own
// mutex serializes every ClaimTurn call against that instance's connection,
// so this uses two separate store instances (two connections, two mutexes)
// against the same file-backed database -- the real-world shape of the
// scenario (two archied/worker processes or connections sharing one session
// DB).
func TestClaimTurnConcurrentInsertRaceResolvesToOneOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")

	const stores = 2
	const trials = 100

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

	// Each trial races the two independent stores over one turn ID, then waits
	// before starting the next trial. This isolates the same-turn claim race
	// from unrelated 100-way global writer contention.
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
		claims := make([]TurnClaim, stores)
		var wg sync.WaitGroup
		var start sync.WaitGroup
		start.Add(1)
		for i := range stores {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				start.Wait()
				_, claims[i], errs[i] = openers[i].ClaimTurn(ctx, initial)
			}(i)
		}
		start.Done()
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("trial %d, store %d: ClaimTurn() unexpected error = %v", trial, i, err)
			}
		}
		owners := 0
		for _, c := range claims {
			if c == TurnClaimOwned {
				owners++
			}
		}
		if owners != 1 {
			t.Fatalf("trial %d: expected exactly one TurnClaimOwned across %d racers, got %d (claims=%v)", trial, stores, owners, claims)
		}
	}
}
