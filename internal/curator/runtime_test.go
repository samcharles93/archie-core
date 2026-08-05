package curator

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

// --- fake clock ---------------------------------------------------------------

// fakeClock is the runtime's injectable clock: Now plus clock-driven timers
// that fire when Advance moves past their deadline. Loop timing is tested
// with this, never wall-clock sleeps.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c  chan time.Time
	at time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (fc *fakeClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now
}

func (fc *fakeClock) After(d time.Duration) <-chan time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	t := &fakeTimer{c: make(chan time.Time, 1), at: fc.now.Add(d)}
	fc.timers = append(fc.timers, t)
	return t.c
}

// Advance moves the clock forward and fires every timer whose deadline has
// passed, returning how many fired.
func (fc *fakeClock) Advance(d time.Duration) int {
	fc.mu.Lock()
	fc.now = fc.now.Add(d)
	var fired []*fakeTimer
	remain := fc.timers[:0]
	for _, t := range fc.timers {
		if !t.at.After(fc.now) {
			fired = append(fired, t)
		} else {
			remain = append(remain, t)
		}
	}
	fc.timers = remain
	fc.mu.Unlock()
	for _, t := range fired {
		select {
		case t.c <- t.at:
		default:
		}
	}
	return len(fired)
}

// --- helpers ------------------------------------------------------------------

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newRuntimeTestEnv(t *testing.T, clock Clock, cfg RuntimeConfig, curators ...*fakeCurator) (*Registry, *Runtime, *testSink) {
	t.Helper()
	sink := &testSink{}
	reg := NewRegistry(Registrar{Clock: clock, Events: sink})
	for _, c := range curators {
		if err := reg.Register(c); err != nil {
			t.Fatalf("Register(%s) = %v, want nil", c.name, err)
		}
	}
	return reg, NewRuntime(reg, cfg), sink
}

const testInterval = 10 * time.Minute

// --- loop behavior -------------------------------------------------------------

func TestRuntimeWakeRunsPendingPassImmediately(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, OnInput: true})
	c.checkResult = true
	c.passResult = PassResult{Actions: []Action{{Type: "test", Reason: "r"}}}
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	// Pending input at start: the first pass runs without any clock advance.
	waitFor(t, 2*time.Second, "first pass", func() bool { return c.passCalls.Load() == 1 })

	// A nudge before the next interval wakes the curator early: a second
	// pass runs without advancing the clock past the interval.
	rt.Nudge("c")
	waitFor(t, 2*time.Second, "pass on nudge", func() bool { return c.passCalls.Load() == 2 })
	if got := clock.Now(); got.After(time.Unix(0, 0).Add(testInterval)) {
		t.Fatalf("clock advanced to %v; wake must not wait for the interval", got)
	}
}

func TestRuntimeIdleCuratorSleepsUntilInterval(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval})
	c.checkResult = false // nothing pending
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	// Initial check only; nothing due before the interval.
	waitFor(t, 2*time.Second, "initial check", func() bool { return c.checkCalls.Load() >= 1 })
	if c.passCalls.Load() != 0 {
		t.Fatalf("passCalls = %d, want 0 while idle", c.passCalls.Load())
	}

	clock.Advance(testInterval - time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := c.checkCalls.Load(); got != 1 {
		t.Fatalf("checkCalls = %d after %v, want 1 (idle curator must not wake early)", got, testInterval-time.Second)
	}

	clock.Advance(time.Second)
	waitFor(t, 2*time.Second, "check at interval", func() bool { return c.checkCalls.Load() >= 2 })
	if c.passCalls.Load() != 0 {
		t.Fatalf("passCalls = %d, want 0 (Check said nothing pending)", c.passCalls.Load())
	}
}

func TestRuntimeCooldownAfterIdlePass(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, Cooldown: 30 * time.Minute})
	c.checkResult = true
	c.passResult = PassResult{} // no actions: the pass was idle
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "idle pass", func() bool { return c.passCalls.Load() == 1 })

	// The cooldown (30m) outranks the interval (10m): nothing runs at
	// +10m...
	clock.Advance(testInterval)
	time.Sleep(50 * time.Millisecond)
	if got := c.checkCalls.Load(); got != 1 {
		t.Fatalf("checkCalls = %d at +interval, want 1 (cooldown must apply after an idle pass)", got)
	}
	// ...and the next pass runs at +30m.
	clock.Advance(20 * time.Minute)
	waitFor(t, 2*time.Second, "pass after cooldown", func() bool { return c.passCalls.Load() == 2 })
}

func TestRuntimeNextCheckInOverride(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval})
	c.checkResult = true
	c.passResult = PassResult{NextCheckIn: time.Unix(0, 0).Add(5 * time.Minute)}
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "first pass", func() bool { return c.passCalls.Load() == 1 })

	// The pass suggested +5m; neither the 10m interval nor anything else
	// fires before it.
	clock.Advance(4 * time.Minute)
	time.Sleep(50 * time.Millisecond)
	if got := c.checkCalls.Load(); got != 1 {
		t.Fatalf("checkCalls = %d before NextCheckIn, want 1", got)
	}
	clock.Advance(time.Minute)
	waitFor(t, 2*time.Second, "pass at NextCheckIn", func() bool { return c.passCalls.Load() == 2 })

	// The second pass suggests the same absolute time, now in the past.
	// The runtime clamps it to the minimum gap instead of hot-spinning.
	clock.Advance(500 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if got := c.passCalls.Load(); got != 2 {
		t.Fatalf("passCalls = %d before the clamp gap elapses, want 2 (past NextCheckIn must not hot-spin)", got)
	}
	clock.Advance(500 * time.Millisecond)
	waitFor(t, 2*time.Second, "pass after clamp gap", func() bool { return c.passCalls.Load() == 3 })
	if got := c.checkCalls.Load(); got > 10 {
		t.Fatalf("checkCalls = %d; a clamped NextCheckIn must not produce a check burst", got)
	}
}

func TestRuntimeNudgeIgnoredForNonInputCurator(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval}) // OnInput false
	c.checkResult = true
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "first pass", func() bool { return c.passCalls.Load() == 1 })

	rt.Nudge("c")
	time.Sleep(50 * time.Millisecond)
	if got := c.passCalls.Load(); got != 1 {
		t.Fatalf("passCalls = %d after nudge, want 1 (non-input curator ignores nudges)", got)
	}

	// A nudge for an unknown curator is dropped without panicking.
	rt.Nudge("missing")
}

func TestRuntimePassPanicRecovered(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, Cooldown: 5 * time.Minute})
	c.checkResult = true
	c.passPanic = true
	_, rt, sink := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "panicking pass", func() bool { return c.passCalls.Load() == 1 })
	waitFor(t, 2*time.Second, "error event", func() bool { return sink.count(events.KindCuratorError) >= 1 })

	// The loop survives the panic: the next check-in still happens.
	clock.Advance(5 * time.Minute)
	waitFor(t, 2*time.Second, "pass after panic", func() bool { return c.passCalls.Load() == 2 })
}

func TestRuntimeCheckPanicRecovered(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, Cooldown: 5 * time.Minute})
	c.checkPanic = true
	_, rt, sink := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "error event", func() bool { return sink.count(events.KindCuratorError) >= 1 })
	clock.Advance(5 * time.Minute)
	waitFor(t, 2*time.Second, "check after panic", func() bool { return c.checkCalls.Load() >= 2 })
}

func TestRuntimeShutdownCancelsInFlightPass(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval})
	c.checkResult = true
	c.passBlock = make(chan struct{})
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	waitFor(t, 2*time.Second, "blocked pass", func() bool { return c.passCalls.Load() == 1 })

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := rt.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() = %v, want nil (in-flight pass must be cancelled via context)", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Stop() took %v; shutdown must be bounded", elapsed)
	}
}

func TestRuntimeShutdownBoundedDespiteStuckPass(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval})
	c.checkResult = true
	c.passBlock = make(chan struct{})
	c.passIgnoreCtx = true // violates the contract on purpose: Pass ignores ctx
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	waitFor(t, 2*time.Second, "stuck pass", func() bool { return c.passCalls.Load() == 1 })
	t.Cleanup(func() { close(c.passBlock) }) // unstick the goroutine at test end

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := rt.Stop(stopCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Stop() took %v; a stuck pass must not hang shutdown", elapsed)
	}
}

func TestRuntimeMaxConcurrentPasses(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	a := newFake("a", Manifest{Interval: testInterval, OnInput: true})
	a.checkResult = true
	a.passBlock = make(chan struct{})
	b := newFake("b", Manifest{Interval: testInterval, OnInput: true})
	b.checkResult = true
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{MaxConcurrentPasses: 1}, a, b)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			close(a.passBlock)
		}
		_ = rt.Stop(context.Background())
	})

	// a holds the single pass slot, blocked.
	waitFor(t, 2*time.Second, "a's blocked pass", func() bool { return a.passCalls.Load() == 1 })

	// b is woken, but the slot is taken: b's pass count cannot advance
	// past its initial run while a is in flight.
	before := b.passCalls.Load()
	for range 5 {
		rt.Nudge("b")
	}
	time.Sleep(100 * time.Millisecond)
	if got := b.passCalls.Load(); got != before {
		t.Fatalf("b.passCalls = %d while a is blocked (was %d); max concurrent passes = 1", got, before)
	}

	// a finishes; b's queued pass runs.
	close(a.passBlock)
	closed = true
	waitFor(t, 2*time.Second, "b's pass after slot freed", func() bool {
		return b.passCalls.Load() > before
	})
}

func TestRuntimeEmitsRunAndActionEvents(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval})
	c.checkResult = true
	c.passResult = PassResult{Actions: []Action{
		{Type: "skill.updated", Detail: "d1", Reason: "stale"},
		{Type: "skill.pruned", Detail: "d2", Reason: "unused"},
	}}
	_, rt, sink := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "run event", func() bool { return sink.count(events.KindCuratorRun) == 1 })
	waitFor(t, 2*time.Second, "action events", func() bool { return sink.count(events.KindCuratorAction) == 2 })
}

func TestRuntimeLifecycleGuards(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{})

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() before Start = %v, want nil no-op", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	if err := rt.Start(context.Background()); err == nil {
		t.Fatal("second Start() = nil, want error")
	}
}

func TestRuntimePassInputRecordsReasonAndLastPass(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))
	c := newFake("c", Manifest{Interval: testInterval, OnInput: true})
	c.checkResult = true
	_, rt, _ := newRuntimeTestEnv(t, clock, RuntimeConfig{}, c)

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 2*time.Second, "first pass", func() bool { return c.passCalls.Load() == 1 })

	rt.Nudge("c")
	waitFor(t, 2*time.Second, "nudged pass", func() bool { return c.passCalls.Load() == 2 })

	c.inputMu.Lock()
	inputs := slices.Clone(c.inputs)
	c.inputMu.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("pass inputs = %d, want 2", len(inputs))
	}
	if inputs[1].LastPass.IsZero() {
		t.Error("second pass LastPass is zero; want the previous pass time")
	}
}
