package taskactions

import (
	"context"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Every operator action that ends a run's work routes through the one cancel
// path, CancelExecution (docs/prds/execution-tree-state-machine.md,
// "Cancellation"): the store records the cancellation first, then the daemon
// cancels the in-memory context that was delivering the run. The actions'
// old Transition-then-interrupt combinations are gone, not alongside.

type cancelRecordingStore struct {
	fakeStore
	cancels []cancelCall
}

type cancelCall struct {
	taskID     int64
	reason, to string
}

func (f *cancelRecordingStore) CancelExecution(_ context.Context, taskID int64, reason, to string) ([]int64, error) {
	f.cancels = append(f.cancels, cancelCall{taskID: taskID, reason: reason, to: to})
	return nil, nil
}

func TestOperatorActionsRouteThroughCancelExecution(t *testing.T) {
	tests := []struct {
		name   string
		status string
		action taskstate.Action
		wantTo string
	}{
		{name: "stop parks the run", status: taskstate.Running, action: taskstate.ActionStop, wantTo: taskstate.Parked},
		{name: "cancel declines a queued run", status: taskstate.Queued, action: taskstate.ActionCancel, wantTo: taskstate.Declined},
		{name: "abandon declines a parked run", status: taskstate.Parked, action: taskstate.ActionAbandon, wantTo: taskstate.Declined},
		{name: "reject declines a running run", status: taskstate.Running, action: taskstate.ActionReject, wantTo: taskstate.Declined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &cancelRecordingStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: tt.status, IssueNumber: 3}}
			var deliveries []int64
			service := Service{
				Store:      store,
				CancelTask: func(id int64) bool { deliveries = append(deliveries, id); return true },
			}
			if err := service.Apply(context.Background(), nil, humanActor(), 7, tt.action, ActionPayload{}); err != nil {
				t.Fatalf("Apply(%s): %v", tt.action, err)
			}
			if len(store.cancels) != 1 {
				t.Fatalf("CancelExecution calls = %+v, want exactly the one cancel path", store.cancels)
			}
			if call := store.cancels[0]; call.taskID != 7 || call.to != tt.wantTo {
				t.Errorf("CancelExecution = %+v, want task 7 to %q", call, tt.wantTo)
			}
			// The context cancel is the delivery mechanism, not a second
			// store write: it rides the store record, never beside it.
			if tt.status == taskstate.Running && !slices.Contains(deliveries, int64(7)) {
				t.Errorf("deliveries = %v, want the running execution's context cancelled", deliveries)
			}
			// The action's own attribution event is the operator's record;
			// the store wrote the mechanical rows.
			if got := store.last(); got.Kind == "" {
				t.Error("the operator event was not recorded")
			}
		})
	}
}

// A stop's detail names the delivery, as before: a context cancel that found
// nothing executing says so, and the parked execution is recorded either way.
func TestStopRecordsEvenWhenNothingWasExecuting(t *testing.T) {
	store := &cancelRecordingStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: taskstate.Running}}
	service := Service{
		Store:      store,
		CancelTask: func(int64) bool { return false },
	}
	if err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionStop, ActionPayload{}); err != nil {
		t.Fatalf("Apply(stop): %v", err)
	}
	if len(store.cancels) != 1 || store.cancels[0].to != taskstate.Parked {
		t.Fatalf("CancelExecution = %+v, want task 7 parked", store.cancels)
	}
	if got := store.last(); got.Kind != events.KindTaskStopped || got.Detail == "" {
		t.Fatalf("stop event = %+v, want the stop kind and the no-execution detail", got)
	}
}
