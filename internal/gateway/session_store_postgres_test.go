package gateway

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m))
}

// newPostgresPool opens a pool to a fresh database with the full schema
// applied and registers pool cleanup.
func newPostgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.Open(t.Context(), pgtest.URL(t))
	if err != nil {
		t.Fatalf("postgres.Open: %v", err)
	}
	if err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		pool.Close()
		t.Fatalf("postgres.Migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newPostgresStore returns a fresh PostgreSQL SessionStore with the gateway
// schema applied.
func newPostgresStore(t *testing.T) SessionStore {
	t.Helper()
	return NewPostgresSessionStore(newPostgresPool(t))
}

// TestSessionStoreConformancePostgres runs the shared SessionStore contract
// against the PostgreSQL implementation.
func TestSessionStoreConformancePostgres(t *testing.T) {
	runSessionStoreSuite(t, newPostgresStore)
}

// TestPostgresMessageTimestampsIncreaseAcrossStoreHandles pins the clamp is
// SQL-atomic, not mutex-derived: two independent stores (two connections)
// appending concurrently to one session must produce strictly increasing
// timestamps. The per-session advisory lock serialises the appends so the
// INSERT ... SELECT MAX(ts) clamp sees the prior committed append.
func TestPostgresMessageTimestampsIncreaseAcrossStoreHandles(t *testing.T) {
	pool := newPostgresPool(t)
	stores := make([]SessionStore, 2)
	for i := range stores {
		stores[i] = NewPostgresSessionStore(pool)
	}

	const perStore = 100
	wantTime := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for storeIndex, st := range stores {
		for messageIndex := range perStore {
			wg.Go(func() {
				err := st.SaveMessage(t.Context(), "sess", messaging.Message{
					ID:     messaging.MessageID(fmt.Sprintf("%d-%d", storeIndex, messageIndex)),
					Sender: "user", Role: messaging.RoleUser,
					Text: "concurrent",
					At:   wantTime,
				})
				if err != nil {
					t.Errorf("SaveMessage: %v", err)
				}
			})
		}
	}
	wg.Wait()

	messages, err := stores[0].RecentMessages(t.Context(), "sess", perStore*len(stores))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != perStore*len(stores) {
		t.Fatalf("messages = %d, want %d", len(messages), perStore*len(stores))
	}
	for i := 1; i < len(messages); i++ {
		if !messages[i].At.After(messages[i-1].At) {
			t.Fatalf("timestamp %d (%s) is not after timestamp %d (%s)", i, messages[i].At, i-1, messages[i-1].At)
		}
	}
}

// TestPostgresClaimTurnConcurrentInsertRaceResolvesToOneOwner pins the
// insert-first claim: racing two independent stores over one turn ID must
// yield exactly one TurnClaimOwned, never two.
func TestPostgresClaimTurnConcurrentInsertRaceResolvesToOneOwner(t *testing.T) {
	pool := newPostgresPool(t)

	const stores = 2
	const trials = 100

	ledgers := make([]TurnLedger, stores)
	for i := range ledgers {
		store, ok := NewPostgresSessionStore(pool).(TurnLedger)
		if !ok {
			t.Fatalf("store[%d] is %T, want TurnLedger", i, store)
		}
		ledgers[i] = store
	}

	ctx := context.Background()
	now := time.UnixMilli(1000).UTC()

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
				_, claims[i], errs[i] = ledgers[i].ClaimTurn(ctx, initial)
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

// TestPostgresSessionStoreRecentTurnsReturnsReplayableToolHistory pins the
// TurnLedger/TurnHistory round-trip: a completed turn reads back with its
// tool calls and response text intact.
func TestPostgresSessionStoreRecentTurnsReturnsReplayableToolHistory(t *testing.T) {
	st := newPostgresStore(t)
	ledger, ok := st.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", st)
	}
	turn, claim, err := ledger.ClaimTurn(t.Context(), TurnRecord{
		TurnID: "turn-history", SessionID: "session-history", SourceID: "source-history", OwnerID: "owner",
		ToolCalls: []ToolCallEvent{{Name: "shell", Output: "exit 0"}},
	})
	if err != nil || claim != TurnClaimOwned {
		t.Fatalf("ClaimTurn() = %#v, %v, %v", turn, claim, err)
	}
	turn.Status = TurnStatusCompleted
	turn.ResponseText = "answer"
	turn.UpdatedAt = time.Now().UTC()
	if err := ledger.SaveTurn(t.Context(), turn); err != nil {
		t.Fatalf("SaveTurn() error = %v", err)
	}
	history, ok := st.(TurnHistory)
	if !ok {
		t.Fatalf("store is %T, want TurnHistory", st)
	}
	got, err := history.RecentTurns(t.Context(), "session-history", 10)
	if err != nil || len(got) != 1 || len(got[0].ToolCalls) != 1 || got[0].ResponseText != "answer" {
		t.Fatalf("RecentTurns() = %#v, %v; want one replayable turn", got, err)
	}
}
