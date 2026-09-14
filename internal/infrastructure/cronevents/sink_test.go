package cronevents

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
)

// TestSinkPublishesEmitAsJobEvent: the engine's emitted event is the only
// trace a scheduled run leaves (a job has no task, no chat turn and no
// workflow behind it), so the adapter has to carry Kind, Detail and Data
// across unchanged and must not borrow a task-scoped identity it does not
// have.
func TestSinkPublishesEmitAsJobEvent(t *testing.T) {
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	sub := bus.Subscribe(4)

	sink := New(bus)
	if sink == nil {
		t.Fatal("New(bus) = nil, want a sink over the bus")
	}

	// The shape the engine builds for a completed run
	// (internal/domain/scheduling/ticker.go runJob).
	sink.Emit(scheduling.KindJobRun, "nightly status", map[string]any{
		"job":         "nightly-status",
		"pool":        string(scheduling.PoolSequential),
		"duration_ms": int64(12),
	})

	select {
	case got := <-sub.C:
		if got.Kind != scheduling.KindJobRun {
			t.Errorf("Kind = %q, want %q", got.Kind, scheduling.KindJobRun)
		}
		if got.Detail != "nightly status" {
			t.Errorf("Detail = %q, want unchanged \"nightly status\"", got.Detail)
		}
		if got.Data["job"] != "nightly-status" {
			t.Errorf("Data[job] = %v, want unchanged \"nightly-status\"", got.Data["job"])
		}
		if got.Data["duration_ms"] != int64(12) {
			t.Errorf("Data[duration_ms] = %v, want unchanged 12", got.Data["duration_ms"])
		}
		// The bus stamps publish time when the event carries none.
		if got.At.IsZero() {
			t.Error("At is zero: a timeline cannot place an event with no timestamp")
		}
		if got.TaskID != 0 || got.Repo != "" || got.Issue != 0 || got.Workflow != "" || got.Stage != "" {
			t.Errorf("event carries task-scoped identity it has none of: %+v", got)
		}
	default:
		t.Fatal("Emit published no event: the dashboard timeline would see nothing")
	}
}

// A missing bus cannot produce an adapter; composition decides whether that
// is acceptable for the selected event mode.
func TestSinkIsNilWithoutABus(t *testing.T) {
	sink := New(nil)
	if sink != nil {
		t.Errorf("New(nil) = %#v, want a nil sink", sink)
	}
}
