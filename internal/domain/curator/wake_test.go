package curator

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

// TestWakeOnPrimaryInputForwardsOnlyPrimaryKinds is the trigger-accounting
// regression guard (archie-core-035).
//
// Rationale (do not delete): if derived curator output counted toward the
// condition that schedules the next curator run, a curator that writes
// memories would schedule itself again — inflating the observation
// threshold and creating a feedback loop. The wake path therefore forwards
// ONLY primary-input kinds; curator-produced activity (curator_run,
// curator_action, curator_error, and any future derived-write kind) must
// never reach it. This test fails if a derived kind is added to the
// forwarded set or the filter is removed as "redundant".
func TestWakeOnPrimaryInputForwardsOnlyPrimaryKinds(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, OnInput: true})
	c.checkResult = true
	reg := NewRegistry(Registrar{Clock: clock, Events: &testSink{}})
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register = %v, want nil", err)
	}
	rt := NewRuntime(reg, RuntimeConfig{})
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	bus := events.NewBus()
	t.Cleanup(bus.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	WakeOnPrimaryInput(ctx, bus, rt, events.KindTurnCompleted)

	// The initial check runs at start; the curator passes because input
	// is pending.
	waitFor(t, 2*time.Second, "initial pass", func() bool { return c.passCalls.Load() == 1 })

	// Derived curator output and unrelated events must not wake the
	// curator: no check, no pass.
	for range 3 {
		bus.Publish(events.Event{Kind: events.KindCuratorRun})
		bus.Publish(events.Event{Kind: events.KindCuratorAction})
		bus.Publish(events.Event{Kind: events.KindCuratorError})
		bus.Publish(events.Event{Kind: events.KindTaskQueued})
	}
	time.Sleep(50 * time.Millisecond)
	if got := c.passCalls.Load(); got != 1 {
		t.Fatalf("passCalls = %d after derived events, want 1 (derived output must not feed its own trigger)", got)
	}
	if got := c.checkCalls.Load(); got != 1 {
		t.Fatalf("checkCalls = %d after derived events, want 1", got)
	}

	// Primary input still wakes the curator.
	bus.Publish(events.Event{Kind: events.KindTurnCompleted, Detail: "session-1"})
	waitFor(t, 2*time.Second, "pass on primary turn", func() bool { return c.passCalls.Load() == 2 })
}

func TestWakeOnPrimaryInputStopsWhenBusCloses(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, OnInput: true})
	c.checkResult = true
	reg := NewRegistry(Registrar{Clock: clock, Events: &testSink{}})
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register = %v, want nil", err)
	}
	rt := NewRuntime(reg, RuntimeConfig{})
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	WakeOnPrimaryInput(ctx, bus, rt, events.KindTurnCompleted)

	// Closing the bus must end the forwarder cleanly (no spin on the
	// closed subscriber channel); cancelling the context must too.
	bus.Close()
	cancel()
	time.Sleep(50 * time.Millisecond)
}
