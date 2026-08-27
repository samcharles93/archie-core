package curator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

const (
	defaultPassTimeout = 10 * time.Minute
	defaultWakeBuffer  = 64
	// minPassGap is the minimum time between the end of one pass and the
	// start of the next. It exists so a curator that suggests a past or
	// immediate NextCheckIn cannot hot-spin the loop; nudges still wake a
	// loop early, so pending input is never delayed by it.
	minPassGap = time.Second
)

// RuntimeConfig tunes the curator runtime. All fields are optional; zero
// values use the documented defaults.
type RuntimeConfig struct {
	// PassTimeout bounds a single pass via context deadline. Zero uses
	// defaultPassTimeout (10m).
	PassTimeout time.Duration
	// MaxConcurrentPasses caps simultaneous passes across all curators.
	// Zero uses 1: passes serialize behind a shared slot so a pass burst
	// can never saturate the model runtime against chat turns.
	MaxConcurrentPasses int
}

// Runtime runs the registered curators' loops: one goroutine per curator,
// timing driven by the registry's clock (fake-clock testable), waking on
// best-effort nudges (the trigger decision belongs to the curator's Check),
// with per-pass timeouts, panic recovery, and bounded shutdown.
//
// Curators are peers, never dependencies: nothing a curator does — a
// blocking pass, a panic, a burst of nudges — can block a chat turn, an
// agent run, or the daemon. Wake events are accelerations; the trigger is a
// deterministic state read at pass time (archie-core-035), so a dropped
// nudge only delays a run to the next check-in.
type Runtime struct {
	registry *Registry
	cfg      RuntimeConfig

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	nudges  map[string]chan struct{}
	sem     chan struct{}
}

// NewRuntime builds a runtime over the registry. The runtime shares the
// registry's clock (so loop timing is fake-clock testable) and event sink
// (so every run and action stays attributable).
func NewRuntime(registry *Registry, cfg RuntimeConfig) *Runtime {
	if cfg.PassTimeout <= 0 {
		cfg.PassTimeout = defaultPassTimeout
	}
	if cfg.MaxConcurrentPasses <= 0 {
		cfg.MaxConcurrentPasses = 1
	}
	return &Runtime{
		registry: registry,
		cfg:      cfg,
		nudges:   make(map[string]chan struct{}),
		sem:      make(chan struct{}, cfg.MaxConcurrentPasses),
	}
}

// Start launches one loop per registered curator and starts the curators'
// lifecycle. A curator whose Start fails is skipped by the registry and
// reported in the joined error; every other curator still runs. Starting an
// already-started runtime is an error.
func (rt *Runtime) Start(ctx context.Context) error {
	rt.mu.Lock()
	if rt.started {
		rt.mu.Unlock()
		return errors.New("curator runtime already started")
	}
	rt.started = true
	loopCtx, cancel := context.WithCancel(ctx)
	rt.cancel = cancel
	for _, name := range rt.registry.Names() {
		c, _ := rt.registry.Get(name)
		if c.Manifest().OnInput {
			rt.nudges[name] = make(chan struct{}, defaultWakeBuffer)
		}
	}
	rt.mu.Unlock()

	startErr := rt.registry.Start(ctx)
	for _, name := range rt.registry.Names() {
		c, _ := rt.registry.Get(name)
		rt.wg.Add(1)
		go rt.runLoop(loopCtx, name, c)
	}
	return startErr
}

// Nudge is a best-effort wake: input-driven curators check their trigger
// early instead of waiting for the next interval. A full channel or an
// unknown name drops the nudge — waking is an acceleration, never the
// source of truth. An empty curator name nudges every input-driven curator.
func (rt *Runtime) Nudge(curator string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for name, ch := range rt.nudges {
		if curator != "" && curator != name {
			continue
		}
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Stop cancels every loop, waits for in-flight passes to unwind (bounded by
// ctx), then stops the curators' lifecycle in reverse registration order.
// A pass that ignores cancellation cannot hang shutdown beyond ctx: Stop
// returns ctx.Err once the deadline passes. Stopping a runtime that never
// started is a no-op.
func (rt *Runtime) Stop(ctx context.Context) error {
	rt.mu.Lock()
	if !rt.started {
		rt.mu.Unlock()
		return nil
	}
	rt.started = false
	cancel := rt.cancel
	rt.cancel = nil
	rt.mu.Unlock()

	cancel()
	done := make(chan struct{})
	go func() {
		rt.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("curator runtime shutdown: %w", ctx.Err())
	}
	return rt.registry.Stop(ctx)
}

// runLoop is one curator's lifetime: sleep until eligible or nudged, ask
// Check whether input is pending, run a bounded pass when it is, and back
// off to the cooldown after an idle or failed pass. Panics in Check and
// Pass are recovered here; the loop survives them.
func (rt *Runtime) runLoop(ctx context.Context, name string, c CuratorEngine) {
	defer rt.wg.Done()

	m := c.Manifest()
	cooldown := m.Cooldown
	if cooldown <= 0 {
		cooldown = 2 * m.Interval
	}
	clock := rt.registry.Host().Clock
	wakeCh := rt.nudges[name] // nil for non-input curators: the case never fires

	var next time.Time // zero means immediately eligible
	var lastPass time.Time

	for {
		now := clock.Now()
		reason := "check-in"
		if !next.IsZero() && now.Before(next) {
			woke, done := waitNext(ctx, wakeCh, clock.After(next.Sub(now)))
			if done {
				return
			}
			if woke {
				reason = "wake"
			}
		}

		due, err := rt.safeCheck(ctx, c)
		if err != nil {
			rt.fail(name, "check", err)
			next = clock.Now().Add(cooldown)
			continue
		}
		if !due {
			next = clock.Now().Add(m.Interval)
			continue
		}

		if !rt.acquire(ctx) {
			return // shutting down while waiting for a pass slot
		}
		passCtx, cancel := context.WithTimeout(ctx, rt.cfg.PassTimeout)
		result, perr := rt.safePass(passCtx, c, PassInput{Reason: reason, LastPass: lastPass})
		cancel()
		rt.release()

		now = clock.Now()
		lastPass = now
		if perr != nil {
			rt.fail(name, "pass", perr)
			next = now.Add(cooldown)
		} else {
			next = scheduleNext(now, m, cooldown, result)
			rt.emitRun(name, now, result)
		}
	}
}

// waitNext blocks until a nudge, the sleep timer, or shutdown. woke means
// a nudge fired — re-check the trigger now; done means the context was
// cancelled — exit the loop.
func waitNext(ctx context.Context, wakeCh <-chan struct{}, timer <-chan time.Time) (woke, done bool) {
	select {
	case <-ctx.Done():
		return false, true
	case <-wakeCh:
		return true, false
	case <-timer:
		return false, false
	}
}

// scheduleNext computes the next eligible time after a successful pass. A
// curator-suggested NextCheckIn in the past (or immediate) would hot-spin
// the loop, so it is clamped to the minimum pass gap; an idle pass (no
// actions) backs off to the cooldown.
func scheduleNext(now time.Time, m Manifest, cooldown time.Duration, result PassResult) time.Time {
	switch {
	case !result.NextCheckIn.IsZero():
		next := result.NextCheckIn
		if earliest := now.Add(minPassGap); next.Before(earliest) {
			next = earliest
		}
		return next
	case len(result.Actions) == 0:
		return now.Add(cooldown)
	default:
		return now.Add(m.Interval)
	}
}

func (rt *Runtime) acquire(ctx context.Context) bool {
	select {
	case rt.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (rt *Runtime) release() { <-rt.sem }

func (rt *Runtime) safeCheck(ctx context.Context, c CuratorEngine) (due bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("check panic: %v", p)
		}
	}()
	return c.Check(ctx)
}

func (rt *Runtime) safePass(ctx context.Context, c CuratorEngine, in PassInput) (result PassResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("pass panic: %v", p)
		}
	}()
	return c.Pass(ctx, in)
}

// fail records a Check/Pass failure on the event stream. The event bus is
// non-blocking by contract (bounded dropping buffers), so even a failing
// curator cannot backpressure anything.
func (rt *Runtime) fail(name, phase string, err error) {
	if sink := rt.registry.Host().Events; sink != nil {
		sink.Emit(events.KindCuratorError, name, map[string]any{
			"curator": name,
			"phase":   phase,
			"err":     err.Error(),
		})
	}
}

// emitRun publishes one run summary plus one event per action, so what ran,
// when, what it changed, and why all stay attributable (archie-core-114).
func (rt *Runtime) emitRun(name string, at time.Time, result PassResult) {
	rt.registry.RecordActivity(name, at, result.Actions)

	sink := rt.registry.Host().Events
	if sink == nil {
		return
	}
	sink.Emit(events.KindCuratorRun, name, map[string]any{
		"curator": name,
		"actions": len(result.Actions),
		"at":      at,
	})
	for _, a := range result.Actions {
		sink.Emit(events.KindCuratorAction, name, map[string]any{
			"curator": name,
			"type":    a.Type,
			"detail":  a.Detail,
			"reason":  a.Reason,
			"at":      a.At,
		})
	}
}
