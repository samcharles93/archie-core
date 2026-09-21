package playbook

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// fakePlaybookLedger is an in-memory PlaybookDispatcher for the coordinator
// helper tests: it records each structural key and reports a duplicate as
// storecontract.ErrAlreadyDispatched, exactly as the SQL INSERT OR IGNORE
// path does.
type fakePlaybookLedger struct {
	records []playbookRecord
}

type playbookRecord struct {
	playbookID      string
	playbookVersion string
	eventID         string
	actionID        string
}

func (f *fakePlaybookLedger) RecordPlaybookDispatch(ctx context.Context, playbookID, playbookVersion, eventID, actionID string) error {
	key := playbookRecord{playbookID, playbookVersion, eventID, actionID}
	if slices.Contains(f.records, key) {
		return storecontract.ErrAlreadyDispatched
	}
	f.records = append(f.records, key)
	return nil
}

func (f *fakePlaybookLedger) DeletePlaybookDispatches(ctx context.Context, playbookID string) error {
	return nil
}

func TestInvokeOnceSkipsRedeliveredAction(t *testing.T) {
	ledger := &fakePlaybookLedger{}
	decision := Decision{PlaybookID: "pb.yaml", Version: "v1", Workflow: "notify", ActionID: "notify"}
	input := DispatchInput{TaskID: "archie:acme/widget/7"}
	calls := 0
	action := func(ctx context.Context) error { calls++; return nil }

	log := slog.New(slog.DiscardHandler)
	if err := InvokeOnce(t.Context(), ledger, log, decision, input, action); err != nil {
		t.Fatalf("first InvokeOnce: %v", err)
	}
	if err := InvokeOnce(t.Context(), ledger, log, decision, input, action); err != nil {
		t.Fatalf("second InvokeOnce: %v", err)
	}
	if calls != 1 {
		t.Fatalf("action invoked %d times, want 1 (a redelivered action must be skipped)", calls)
	}
}

func TestInvokeOnceNilLoggerOnRedelivery(t *testing.T) {
	ledger := &fakePlaybookLedger{}
	decision := Decision{PlaybookID: "pb.yaml", Version: "v1", Workflow: "notify", ActionID: "notify"}
	input := DispatchInput{TaskID: "archie:acme/widget/7"}

	if err := InvokeOnce(t.Context(), ledger, nil, decision, input, func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("first InvokeOnce: %v", err)
	}
	if err := InvokeOnce(t.Context(), ledger, nil, decision, input, func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("redelivered InvokeOnce with nil logger: %v", err)
	}
}

func TestInvokeOnceFallsBackToActionPosition(t *testing.T) {
	ledger := &fakePlaybookLedger{}
	decision := Decision{PlaybookID: "pb.yaml", Version: "v1", Workflow: "notify", ActionPosition: 2}
	input := DispatchInput{TaskID: "archie:acme/widget/7"}

	if err := InvokeOnce(t.Context(), ledger, slog.New(slog.DiscardHandler), decision, input, func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("InvokeOnce: %v", err)
	}
	if len(ledger.records) != 1 {
		t.Fatalf("ledger records = %d, want 1", len(ledger.records))
	}
	got := ledger.records[0]
	want := playbookRecord{playbookID: "pb.yaml", playbookVersion: "v1", eventID: "archie:acme/widget/7", actionID: "2"}
	if got != want {
		t.Fatalf("ledger record = %+v, want %+v (an action without an id keys on its 1-based position)", got, want)
	}
}

func TestInvokeOnceRejectsEmptyKeyComponent(t *testing.T) {
	ledger := &fakePlaybookLedger{}
	decision := Decision{PlaybookID: "pb.yaml", Version: "v1", Workflow: "notify", ActionID: "notify"}
	input := DispatchInput{TaskID: ""}

	err := InvokeOnce(t.Context(), ledger, slog.New(slog.DiscardHandler), decision, input, func(ctx context.Context) error { return nil })
	if err == nil {
		t.Fatal("InvokeOnce with empty event_id should error before recording")
	}
	if len(ledger.records) != 0 {
		t.Fatalf("ledger records = %d, want 0 (validation must precede the ledger write)", len(ledger.records))
	}
}
