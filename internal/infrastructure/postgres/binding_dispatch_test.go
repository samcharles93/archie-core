package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// A capture a binding evaluated without dispatching is recorded once and not
// offered to that binding again; a later dispatch of the pair is refused.
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
