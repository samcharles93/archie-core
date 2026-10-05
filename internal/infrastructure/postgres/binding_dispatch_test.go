package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// A capture a binding evaluated without dispatching is recorded once and not
// offered to that binding again; a later dispatch of the pair is refused. A
// dispatch records the task it started, and the ledger lists by any end.
func TestEvaluatedCaptureIsNotReoffered(t *testing.T) {
	ctx := t.Context()
	eda := pgstore.EDA(t, nil)

	eventTypeID, err := eda.InsertEventType(ctx, eventtype.EventType{Source: "forge", Name: "push"})
	if err != nil {
		t.Fatal(err)
	}
	mappingID, err := eda.InsertMapping(ctx, mapping.Mapping{Name: "push", EventTypeID: eventTypeID})
	if err != nil {
		t.Fatal(err)
	}
	bindingID, err := eda.InsertBinding(ctx, binding.Binding{
		Name: "on-push", Matcher: binding.Matcher{Source: "forge"}, MappingID: mappingID, Workflow: "tdd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eda.ApproveBinding(ctx, bindingID); err != nil {
		t.Fatal(err)
	}
	captureID, err := eda.InsertCapture(ctx, storecontract.CapturedEvent{
		Source: "forge", Body: "{}", Authenticated: true, ReceivedAt: time.Now(),
	}, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}

	assertUndispatched(t, eda, 1)
	if err := eda.RecordDispatch(ctx, bindingID, 1, captureID, 0, "filtered"); err != nil {
		t.Fatalf("record filtered evaluation: %v", err)
	}
	assertUndispatched(t, eda, 0)
	if err := eda.RecordDispatch(ctx, bindingID, 1, captureID, 0, ""); !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		t.Fatalf("dispatch after a filtered evaluation = %v, want ErrAlreadyDispatched", err)
	}

	// A second capture dispatches; the ledger links it to the task it started.
	dispatched, err := eda.InsertCapture(ctx, storecontract.CapturedEvent{
		Source: "forge", Body: "{}", Authenticated: true, ReceivedAt: time.Now(),
	}, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := eda.RecordDispatch(ctx, bindingID, 1, dispatched, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := eda.SetDispatchTask(ctx, bindingID, dispatched, 42); err != nil {
		t.Fatal(err)
	}
	rows, err := eda.ListDispatches(ctx, storecontract.DispatchFilter{TaskID: 42, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CaptureID != dispatched || rows[0].BindingID != bindingID {
		t.Fatalf("dispatches for task 42 = %+v, want the dispatched capture", rows)
	}
	all, err := eda.ListDispatches(ctx, storecontract.DispatchFilter{BindingID: bindingID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("dispatches for the binding = %d, want 2", len(all))
	}
}

func assertUndispatched(t *testing.T, eda *postgres.EDA, want int) {
	t.Helper()
	got, err := eda.ListUndispatchedCaptures(t.Context(), []string{"forge"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != want {
		t.Fatalf("undispatched captures = %d, want %d", len(got), want)
	}
}

// A binding moves pending -> armed -> paused -> pending, and refuses any other
// transition; a paused binding is offered no captures.
func TestBindingLifecycle(t *testing.T) {
	ctx := t.Context()
	eda := pgstore.EDA(t, nil)
	eventTypeID, err := eda.InsertEventType(ctx, eventtype.EventType{Source: "forge", Name: "push"})
	if err != nil {
		t.Fatal(err)
	}
	mappingID, err := eda.InsertMapping(ctx, mapping.Mapping{Name: "push", EventTypeID: eventTypeID})
	if err != nil {
		t.Fatal(err)
	}
	id, err := eda.InsertBinding(ctx, binding.Binding{
		Name: "on-push", Matcher: binding.Matcher{Source: "forge"}, MappingID: mappingID, Workflow: "tdd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eda.InsertCapture(ctx, storecontract.CapturedEvent{
		Source: "forge", Body: "{}", Authenticated: true, ReceivedAt: time.Now(),
	}, time.Hour, 100); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		name    string
		apply   func(context.Context, string) error
		wantErr error
		status  binding.Status
		offered int
	}{
		{"a pending binding cannot pause", eda.PauseBinding, storecontract.ErrBindingTransition, binding.StatusPendingApproval, 0},
		{"approve arms it", eda.ApproveBinding, nil, binding.StatusArmed, 1},
		{"an armed binding cannot resume", eda.ResumeBinding, storecontract.ErrBindingTransition, binding.StatusArmed, 1},
		{"pause stops it", eda.PauseBinding, nil, binding.StatusPaused, 0},
		{"a paused binding cannot be approved", eda.ApproveBinding, storecontract.ErrBindingTransition, binding.StatusPaused, 0},
		{"resume returns it to approval", eda.ResumeBinding, nil, binding.StatusPendingApproval, 0},
	}
	for _, step := range steps {
		if err := step.apply(ctx, id); !errors.Is(err, step.wantErr) {
			t.Fatalf("%s: err = %v, want %v", step.name, err, step.wantErr)
		}
		b, err := eda.GetBinding(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if b.Status != step.status {
			t.Fatalf("%s: status = %q, want %q", step.name, b.Status, step.status)
		}
		assertUndispatched(t, eda, step.offered)
	}
	if err := eda.PauseBinding(ctx, "missing"); !errors.Is(err, storecontract.ErrBindingNotFound) {
		t.Fatalf("pause of a missing binding = %v, want ErrBindingNotFound", err)
	}

	// A source is not deleted while an armed binding fires on it.
	if err := eda.InsertSource(ctx, source.Source{Path: "forge", Signing: source.SigningSigned}); err != nil {
		t.Fatal(err)
	}
	if err := eda.ApproveBinding(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := eda.DeleteSource(ctx, "forge"); !errors.Is(err, storecontract.ErrSourceInUse) {
		t.Fatalf("delete of a source an armed binding fires on = %v, want ErrSourceInUse", err)
	}
	if err := eda.PauseBinding(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := eda.DeleteSource(ctx, "forge"); err != nil {
		t.Fatalf("delete of a source with no armed binding = %v", err)
	}
	if err := eda.DeleteSource(ctx, "forge"); !errors.Is(err, storecontract.ErrSourceNotFound) {
		t.Fatalf("second delete = %v, want ErrSourceNotFound", err)
	}
}
