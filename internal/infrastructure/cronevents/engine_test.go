package cronevents

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
)

// chanClock is a deterministic scheduling.Clock: one tick per value sent on
// ch, so the test drives the engine instead of waiting on wall-clock time.
// After returns the same channel every time, and the engine re-arms only when
// it is called again -- so nothing fires unless the test says so.
type chanClock struct{ ch chan time.Time }

func (c chanClock) Now() time.Time { return time.Time{} }

func (c chanClock) After(time.Duration) <-chan time.Time { return c.ch }

// staticSource reports the same due jobs on every tick.
type staticSource []scheduling.Job

func (s staticSource) Due(context.Context, time.Time) ([]scheduling.Job, error) { return s, nil }

type okRunner struct{}

func (okRunner) Run(context.Context, scheduling.Job) error { return nil }

// TestEngineSinkPublishesRealRunOnTheBus is the end-to-end proof of the
// adapter: the engine's own emission, over the bus, as an event the
// dashboard's timeline can read. It runs a real engine on a driven clock --
// no sleeps, no wall-clock deadlines -- so the only thing under test is the
// wiring between Emit and Publish.
func TestEngineSinkPublishesRealRunOnTheBus(t *testing.T) {
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	sub := bus.Subscribe(4)

	sink := New(bus)
	clock := chanClock{ch: make(chan time.Time, 1)}
	engine, err := scheduling.NewEngine(
		staticSource{{ID: "daily", Pool: scheduling.PoolParallel, Detail: "daily status summary"}},
		okRunner{},
		scheduling.EngineConfig{Interval: time.Minute, Clock: clock, Events: sink},
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		// WithoutCancel: t.Context() is cancelled before this cleanup runs,
		// and a cancelled ctx would only make Stop give up at once.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		if err := engine.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	clock.ch <- time.Time{} // one tick

	select {
	case got := <-sub.C:
		if got.Kind != scheduling.KindJobRun {
			t.Errorf("Kind = %q, want %q", got.Kind, scheduling.KindJobRun)
		}
		if got.Detail != "daily status summary" {
			t.Errorf("Detail = %q, want the engine's job detail", got.Detail)
		}
		if got.Data["job"] != "daily" {
			t.Errorf("Data[job] = %v, want %q", got.Data["job"], "daily")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no job_run event reached the bus: the engine's run is unobservable")
	}
}
