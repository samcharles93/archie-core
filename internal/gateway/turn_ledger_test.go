package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTurnLedgerClaimLifecycleIsIdempotent(t *testing.T) {
	tests := []struct {
		name string
		open func(t *testing.T) (TurnLedger, func())
	}{
		{
			name: "sqlite memory",
			open: func(t *testing.T) (TurnLedger, func()) {
				store, err := NewSQLiteSessionStoreMemory()
				if err != nil {
					t.Fatal(err)
				}
				ledger, ok := store.(TurnLedger)
				if !ok {
					t.Fatalf("store is %T, want TurnLedger", store)
				}
				return ledger, func() { _ = store.Close() }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger, cleanup := tt.open(t)
			t.Cleanup(cleanup)
			ctx := context.Background()
			now := time.UnixMilli(1000).UTC()
			initial := TurnRecord{
				TurnID:    CanonicalTurnID("session-1", "source-1"),
				SessionID: "session-1",
				SourceID:  "source-1",
				Status:    TurnStatusAccepted,
				CreatedAt: now,
				UpdatedAt: now,
			}

			claimed, result, err := ledger.ClaimTurn(ctx, initial)
			if err != nil {
				t.Fatalf("first ClaimTurn() error = %v", err)
			}
			if result != TurnClaimOwned || claimed.Status != TurnStatusRunning || claimed.Attempt != 1 {
				t.Fatalf("first claim = %#v, result %q", claimed, result)
			}

			inFlight, result, err := ledger.ClaimTurn(ctx, initial)
			if err != nil {
				t.Fatalf("in-flight ClaimTurn() error = %v", err)
			}
			if result != TurnClaimInProgress || inFlight.TurnID != initial.TurnID {
				t.Fatalf("in-flight claim = %#v, result %q", inFlight, result)
			}

			claimed.Status = TurnStatusCompleted
			claimed.ResponseText = "answer"
			claimed.AssistantMessageID = "assistant-1"
			claimed.UpdatedAt = now.Add(time.Second)
			if err := ledger.SaveTurn(ctx, claimed); err != nil {
				t.Fatalf("SaveTurn(completed) error = %v", err)
			}

			completed, result, err := ledger.ClaimTurn(ctx, initial)
			if err != nil {
				t.Fatalf("completed ClaimTurn() error = %v", err)
			}
			if result != TurnClaimCompleted || completed.ResponseText != "answer" {
				t.Fatalf("completed claim = %#v, result %q", completed, result)
			}

			completed.Status = TurnStatusFailed
			completed.AssistantMessageID = ""
			completed.ResponseText = ""
			completed.Error = "provider unavailable"
			completed.UpdatedAt = now.Add(2 * time.Second)
			if err := ledger.SaveTurn(ctx, completed); err != nil {
				t.Fatalf("SaveTurn(failed) error = %v", err)
			}
			retry, result, err := ledger.ClaimTurn(ctx, initial)
			if err != nil {
				t.Fatalf("retry ClaimTurn() error = %v", err)
			}
			if result != TurnClaimOwned || retry.Status != TurnStatusRunning || retry.Attempt != 2 {
				t.Fatalf("retry claim = %#v, result %q", retry, result)
			}

			recoverable, err := ledger.ListRecoverableTurns(ctx)
			if err != nil {
				t.Fatalf("ListRecoverableTurns() error = %v", err)
			}
			if len(recoverable) != 1 || recoverable[0].TurnID != initial.TurnID {
				t.Fatalf("recoverable turns = %#v, want retried turn", recoverable)
			}
		})
	}
}

func TestTurnLedgerReclaimsAcrossProcessOwnersAndRejectsStaleWrites(t *testing.T) {
	tests := []struct {
		name string
		open func(t *testing.T) (TurnLedger, func())
	}{
		{
			name: "sqlite memory",
			open: func(t *testing.T) (TurnLedger, func()) {
				store, err := NewSQLiteSessionStoreMemory()
				if err != nil {
					t.Fatal(err)
				}
				ledger, ok := store.(TurnLedger)
				if !ok {
					t.Fatalf("store is %T, want TurnLedger", store)
				}
				return ledger, func() { _ = store.Close() }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger, cleanup := tt.open(t)
			t.Cleanup(cleanup)
			ctx := context.Background()
			initial := TurnRecord{
				TurnID:    CanonicalTurnID("session-owner", "source-owner"),
				SessionID: "session-owner",
				SourceID:  "source-owner",
				OwnerID:   "process-a",
				Status:    TurnStatusAccepted,
			}
			old, claim, err := ledger.ClaimTurn(ctx, initial)
			if err != nil || claim != TurnClaimOwned {
				t.Fatalf("first claim = %#v, %q, %v", old, claim, err)
			}
			newOwner := initial
			newOwner.OwnerID = "process-b"
			inProgress, claim, err := ledger.ClaimTurn(ctx, newOwner)
			if err != nil || claim != TurnClaimInProgress {
				t.Fatalf("cross-owner claim = %#v, %q, %v; want in progress", inProgress, claim, err)
			}
			if err := ledger.RecoverTurns(ctx, "process-b"); err != nil {
				t.Fatalf("RecoverTurns() error = %v", err)
			}
			reclaimed, claim, err := ledger.ClaimTurn(ctx, newOwner)
			if err != nil || claim != TurnClaimOwned {
				t.Fatalf("reclaim = %#v, %q, %v", reclaimed, claim, err)
			}
			if reclaimed.Attempt != 2 || reclaimed.OwnerID != "process-b" {
				t.Fatalf("reclaimed turn = %#v, want attempt 2 owned by process-b", reclaimed)
			}
			old.Status = TurnStatusCompleted
			old.ResponseText = "stale response"
			if err := ledger.SaveTurn(ctx, old); !errors.Is(err, ErrTurnOwnershipLost) {
				t.Fatalf("stale SaveTurn() error = %v, want ErrTurnOwnershipLost", err)
			}
		})
	}
}

func TestSessionDeleteRemovesTurns(t *testing.T) {
	tests := []struct {
		name string
		open func(t *testing.T) SessionStore
	}{
		{
			name: "sqlite memory",
			open: func(t *testing.T) SessionStore {
				store, err := NewSQLiteSessionStoreMemory()
				if err != nil {
					t.Fatal(err)
				}
				return store
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.open(t)
			t.Cleanup(func() { _ = store.Close() })
			ledger, ok := store.(TurnLedger)
			if !ok {
				t.Fatalf("store is %T, want TurnLedger", store)
			}
			ctx := context.Background()
			turn := TurnRecord{
				TurnID: CanonicalTurnID("session-delete", "source-delete"), SessionID: "session-delete",
				SourceID: "source-delete", OwnerID: "process-a", Status: TurnStatusAccepted,
			}
			if _, claim, err := ledger.ClaimTurn(ctx, turn); err != nil || claim != TurnClaimOwned {
				t.Fatalf("ClaimTurn() = %q, %v", claim, err)
			}
			if err := store.Delete(ctx, turn.SessionID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, ok, err := ledger.GetTurn(ctx, turn.TurnID); err != nil || ok {
				t.Fatalf("GetTurn() after delete = ok %v, err %v; want absent", ok, err)
			}
		})
	}
}

func TestSQLiteTurnLedgerSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	ctx := context.Background()
	store, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSQLiteSessionStore() error = %v", err)
	}
	ledger, ok := store.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", store)
	}
	turn := TurnRecord{
		TurnID:    CanonicalTurnID("session-reopen", "source-reopen"),
		SessionID: "session-reopen",
		SourceID:  "source-reopen",
		OwnerID:   "process-a",
		Status:    TurnStatusAccepted,
	}
	claimed, claim, err := ledger.ClaimTurn(ctx, turn)
	if err != nil || claim != TurnClaimOwned {
		t.Fatalf("ClaimTurn() = %#v, %q, %v", claimed, claim, err)
	}
	claimed.Status = TurnStatusCompleted
	claimed.ResponseText = "durable response"
	if err := ledger.SaveTurn(ctx, claimed); err != nil {
		t.Fatalf("SaveTurn() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	reopenedLedger, ok := reopened.(TurnLedger)
	if !ok {
		t.Fatalf("reopened store is %T, want TurnLedger", reopened)
	}
	got, ok, err := reopenedLedger.GetTurn(ctx, turn.TurnID)
	if err != nil || !ok {
		t.Fatalf("GetTurn() after reopen = %#v, %v, %v", got, ok, err)
	}
	if got.Status != TurnStatusCompleted || got.ResponseText != "durable response" || got.OwnerID != "process-a" {
		t.Fatalf("reopened turn = %#v, want completed durable record", got)
	}
}

func TestCanonicalTurnIDIsStableAndSessionScoped(t *testing.T) {
	first := CanonicalTurnID("session-1", "source-1")
	if first == "" {
		t.Fatal("CanonicalTurnID() returned empty ID")
	}
	if got := CanonicalTurnID("session-1", "source-1"); got != first {
		t.Fatalf("same key produced %q, want %q", got, first)
	}
	if got := CanonicalTurnID("session-2", "source-1"); got == first {
		t.Fatal("different sessions shared a turn ID")
	}
	if got := CanonicalTurnID("session-1", ""); got != "" {
		t.Fatalf("empty source ID produced %q, want empty", got)
	}
}

func TestCanonicalTurnIDComponentsAreInjectivelyEncoded(t *testing.T) {
	t.Parallel()

	first := CanonicalTurnID("a\x00b", "c")
	second := CanonicalTurnID("a", "b\x00c")
	if first == second {
		t.Fatalf("ambiguous component pairs shared turn ID %q", first)
	}
}

func TestTurnRunnerCanonicalTurnIDPreservesMatchingLegacyRecord(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ledger, ok := store.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", store)
	}
	ctx := t.Context()
	const (
		sessionID = "a\x00b"
		sourceID  = "c"
	)
	legacyID := legacyCanonicalTurnID(sessionID, sourceID)
	if _, claim, err := ledger.ClaimTurn(ctx, TurnRecord{
		TurnID: legacyID, SessionID: sessionID, SourceID: sourceID,
		Status: TurnStatusAccepted, OwnerID: "old-process",
	}); err != nil || claim != TurnClaimOwned {
		t.Fatalf("seed legacy turn = %q, %v", claim, err)
	}

	runner := NewTurnRunner(TurnRunnerConfig{Ledger: ledger})
	got, err := runner.canonicalTurnID(ctx, sessionID, sourceID)
	if err != nil {
		t.Fatalf("canonicalTurnID: %v", err)
	}
	if got != legacyID {
		t.Fatalf("canonicalTurnID = %q, want matching legacy ID %q", got, legacyID)
	}
}

func TestTurnRunnerCanonicalTurnIDRejectsCollidingLegacyRecord(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ledger, ok := store.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", store)
	}
	ctx := t.Context()
	const (
		firstSession  = "a\x00b"
		firstSource   = "c"
		secondSession = "a"
		secondSource  = "b\x00c"
	)
	collidingLegacyID := legacyCanonicalTurnID(firstSession, firstSource)
	if _, claim, err := ledger.ClaimTurn(ctx, TurnRecord{
		TurnID: collidingLegacyID, SessionID: firstSession, SourceID: firstSource,
		Status: TurnStatusAccepted, OwnerID: "first-process",
	}); err != nil || claim != TurnClaimOwned {
		t.Fatalf("seed colliding turn = %q, %v", claim, err)
	}

	runner := NewTurnRunner(TurnRunnerConfig{Ledger: ledger})
	got, err := runner.canonicalTurnID(ctx, secondSession, secondSource)
	if err != nil {
		t.Fatalf("canonicalTurnID: %v", err)
	}
	want := CanonicalTurnID(secondSession, secondSource)
	if got != want || got == collidingLegacyID {
		t.Fatalf("canonicalTurnID = %q, want isolated ID %q (legacy collision %q)",
			got, want, collidingLegacyID)
	}
}
