package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// parkClassRecorder captures the class of the one park a test drives, without
// a live store. The embedded TaskStore is nil: only ParkTask is reachable from
// the code under test, and any other store call panics loudly rather than
// silently succeeding.
type parkClassRecorder struct {
	storecontract.TaskStore
	class taskstate.ParkClass
}

func (r *parkClassRecorder) ParkTask(_ context.Context, _ int64, _, _, class string) error {
	r.class = class
	return nil
}

// TestDurablePinDefectIsNotParkedTransient pins the classification of a pin the
// task itself carries wrong. A stored definition whose YAML does not hash to
// the digest recorded beside it is a durable defect: the same row fails the
// same way on every requeue until an operator repairs it, so ParkTransient --
// whose own contract says "a requeue can succeed" -- would hand a future retry
// pass an endless loop.
func TestDurablePinDefectIsNotParkedTransient(t *testing.T) {
	store := &parkClassRecorder{}
	d := &Daemon{Store: store, Log: slog.New(slog.DiscardHandler)}
	task := &workflow.Task{
		ID:                       1,
		Workflow:                 "implement",
		WorkflowDefinitionYAML:   "id: implement\nsteps:\n  - type: implement.prepare\n",
		WorkflowDefinitionDigest: workflow.DigestDefinition("id: implement\nsteps:\n  - type: implement.build\n"),
	}
	if _, ok := d.pinTaskProfile(context.Background(), task); ok {
		t.Fatal("pinTaskProfile accepted a tampered pin")
	}
	if store.class == "" {
		t.Fatal("a durable pin defect was not parked at all")
	}
	if store.class == taskstate.ParkTransient {
		t.Fatalf("durable pin defect parked as %q: a requeue recurs on the same defect, so a retry pass cannot make progress", store.class)
	}
}

// failingWorkflowDefinitions stands in for a State Store read failure.
type failingWorkflowDefinitions struct{ err error }

func (f failingWorkflowDefinitions) WorkflowDefinitions(context.Context) (workflow.WorkflowDefinitionCollection, int64, error) {
	return workflow.WorkflowDefinitionCollection{}, 0, f.err
}

// TestTransientPinFailureStaysTransient keeps the other half of the cause-based
// rule: a State Store read failure is environmental, so it must still park
// transient rather than being folded in with the durable defects.
func TestTransientPinFailureStaysTransient(t *testing.T) {
	store := &parkClassRecorder{}
	d := &Daemon{
		Store:               store,
		Log:                 slog.New(slog.DiscardHandler),
		WorkflowDefinitions: failingWorkflowDefinitions{err: errors.New("state store unavailable")},
	}
	task := &workflow.Task{ID: 1, Workflow: "implement"}
	if _, ok := d.pinTaskProfile(context.Background(), task); ok {
		t.Fatal("pinTaskProfile succeeded despite a definition read failure")
	}
	if store.class != taskstate.ParkTransient {
		t.Fatalf("pin class = %q, want %q for a State Store read failure", store.class, taskstate.ParkTransient)
	}
}
