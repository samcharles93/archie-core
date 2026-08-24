package scheduling

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

// Defaults for the tunables an operator may leave unset.
const (
	// DefaultInterval is the tick cadence when none is configured. A minute
	// is the finest granularity a cron-style schedule needs, and polling a
	// local job store that often costs nothing.
	DefaultInterval = time.Minute
	// DefaultMaxParallel caps concurrent parallel-pool runs. It exists so a
	// store that reports many jobs due at once (the first tick after a long
	// downtime, typically) cannot launch an unbounded fan-out.
	DefaultMaxParallel = 4
	// DefaultJobTimeout bounds one run. A scheduled job that has not
	// finished in an hour is stuck, and leaving it in flight would suppress
	// every later run of the same job forever.
	DefaultJobTimeout = time.Hour
)

// Event kinds emitted by the engine, re-exported so callers of this package
// need not import the events package to match on them.
const (
	KindJobRun   = events.KindJobRun
	KindJobError = events.KindJobError
)

// Phases reported on KindJobError, naming which step failed.
const (
	phaseSource   = "source"   // JobSource.Due returned an error
	phaseDispatch = "dispatch" // the job was rejected before it ever ran
	phaseRun      = "run"      // Runner.Run failed, panicked or timed out
)

// sequentialQueueDepth bounds the sequential pool's backlog. Overlap
// suppression already caps the backlog at one entry per distinct job, so this
// is a backstop against a pathological source, not the primary bound.
const sequentialQueueDepth = 256

// EngineConfig tunes the ticker engine. Every field is optional; zero values
// take the documented defaults.
type EngineConfig struct {
	// Interval is the tick cadence: how often the engine asks its source
	// what is due. Zero uses DefaultInterval; negative is an error.
	Interval time.Duration
	// MaxParallel caps simultaneous parallel-pool runs. Zero uses
	// DefaultMaxParallel. It does not bound the sequential pool, which is
	// one by definition.
	MaxParallel int
	// JobTimeout bounds a single run via context deadline. Zero uses
	// DefaultJobTimeout.
	JobTimeout time.Duration
	// Clock is injectable time. Nil uses the system clock.
	Clock Clock
	// Events receives run and error events. Nil disables emission; the
	// engine still runs, it is just unobservable.
	Events Sink
}

func (c EngineConfig) validate() error {
	switch {
	case c.Interval < 0:
		return fmt.Errorf("scheduling: interval must not be negative, got %v", c.Interval)
	case c.MaxParallel < 0:
		return fmt.Errorf("scheduling: max parallel must not be negative, got %d", c.MaxParallel)
	case c.JobTimeout < 0:
		return fmt.Errorf("scheduling: job timeout must not be negative, got %v", c.JobTimeout)
	}
	return nil
}

// withDefaults returns the config with zero values replaced.
func (c EngineConfig) withDefaults() EngineConfig {
	if c.Interval == 0 {
		c.Interval = DefaultInterval
	}
	if c.MaxParallel == 0 {
		c.MaxParallel = DefaultMaxParallel
	}
	if c.JobTimeout == 0 {
		c.JobTimeout = DefaultJobTimeout
	}
	if c.Clock == nil {
		c.Clock = systemClock{}
	}
	return c
}

// Engine is the ticker engine: one loop that asks the source what is due at
// each tick and dispatches each due job to the pool it declares.
//
// Two invariants make the timing safe to reason about:
//
//   - A job never overlaps itself. While a run is queued or in flight, later
//     ticks reporting the same ID are skipped, not stacked. A job slower than
//     the tick interval therefore runs back-to-back, never fanned out.
//   - The pools do not interfere. A blocked sequential job cannot delay a
//     parallel one, and a saturated parallel cap cannot delay the sequential
//     worker — they are separate dispatch paths sharing only the tick.
//
// Every failure is contained: a source error, a job error, a job panic and a
// job timeout are each reported as an event and leave the loop running.
type Engine struct {
	source JobSource
	runner Runner
	cfg    EngineConfig

	queue chan Job // sequential pool backlog
	sem   chan struct{}

	mu         sync.Mutex
	started    bool
	stopped    bool
	stopLoop   context.CancelFunc
	cancelRuns context.CancelFunc
	active     map[string]struct{} // queued or in flight, by job ID
	wg         sync.WaitGroup
}

// NewEngine builds the engine over its source and runner. It fails on a nil
// dependency or a negative tunable; zero tunables take their defaults.
func NewEngine(source JobSource, runner Runner, cfg EngineConfig) (*Engine, error) {
	if source == nil {
		return nil, errors.New("scheduling: job source must not be nil")
	}
	if runner == nil {
		return nil, errors.New("scheduling: runner must not be nil")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	return &Engine{
		source: source,
		runner: runner,
		cfg:    cfg,
		queue:  make(chan Job, sequentialQueueDepth),
		sem:    make(chan struct{}, cfg.MaxParallel),
		active: make(map[string]struct{}),
	}, nil
}

// Interval reports the resolved tick cadence.
func (e *Engine) Interval() time.Duration { return e.cfg.Interval }

// MaxParallel reports the resolved parallel-pool cap.
func (e *Engine) MaxParallel() int { return e.cfg.MaxParallel }

// JobTimeout reports the resolved per-run timeout.
func (e *Engine) JobTimeout() time.Duration { return e.cfg.JobTimeout }

// Start launches the tick loop and the sequential worker.
//
// An engine is single-use: starting one that is already started, or that has
// been stopped, is an error. Restarting is refused rather than supported
// because Stop leaves the dropped sequential backlog and the claims of runs
// it did not wait for behind — a restarted engine would re-run stale queued
// jobs and permanently suppress the jobs still marked claimed. Build a new
// engine instead; it is a struct, not a resource.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return errors.New("scheduling: engine already stopped; build a new one")
	}
	if e.started {
		return errors.New("scheduling: engine already started")
	}
	e.started = true

	loopCtx, stopLoop := context.WithCancel(ctx)
	e.stopLoop = stopLoop
	// Run contexts descend from Background, not from loopCtx, so that Stop
	// drains gracefully: cancelling the loop must stop new work without
	// yanking the context out from under a run that is already going. Stop
	// cancels these only once its own deadline passes.
	runCtx, cancelRuns := context.WithCancel(context.WithoutCancel(ctx))
	e.cancelRuns = cancelRuns

	e.wg.Add(2)
	go e.tickLoop(loopCtx, runCtx)
	go e.sequentialWorker(loopCtx, runCtx)
	return nil
}

// Stop halts ticking, drops any queued-but-unstarted runs, and waits for
// in-flight runs to finish, bounded by ctx. If ctx expires first, in-flight
// runs are cancelled and ctx's error is returned — a Runner that ignores
// cancellation can still outlive Stop, which is why the deadline is the
// caller's to set. Stopping an engine that never started is a no-op.
func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil
	}
	e.started = false
	e.stopped = true
	stopLoop, cancelRuns := e.stopLoop, e.cancelRuns
	e.stopLoop, e.cancelRuns = nil, nil
	e.mu.Unlock()

	stopLoop()
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		cancelRuns()
		return nil
	case <-ctx.Done():
		cancelRuns()
		return fmt.Errorf("scheduling: engine shutdown: %w", ctx.Err())
	}
}

// tickLoop sleeps one interval, asks the source what is due, and dispatches
// it. The source is authoritative: the engine applies no schedule arithmetic
// of its own, so what "due" means stays the store's decision.
func (e *Engine) tickLoop(loopCtx, runCtx context.Context) {
	defer e.wg.Done()
	for {
		select {
		case <-loopCtx.Done():
			return
		case <-e.cfg.Clock.After(e.cfg.Interval):
		}

		jobs, err := e.source.Due(loopCtx, e.cfg.Clock.Now())
		if err != nil {
			e.emitError(Job{}, phaseSource, err)
			continue
		}
		for _, job := range jobs {
			e.dispatch(loopCtx, runCtx, job)
		}
	}
}

// dispatch routes one due job to its pool, skipping it if it is invalid or
// already queued or in flight.
func (e *Engine) dispatch(loopCtx, runCtx context.Context, job Job) {
	if err := job.Validate(); err != nil {
		e.emitError(job, phaseDispatch, err)
		return
	}
	if !e.claim(job.ID) {
		return // an earlier run of this job has not finished; skip this tick
	}

	if job.Pool.resolve() == PoolParallel {
		e.wg.Go(func() {
			defer e.release(job.ID)
			select {
			case e.sem <- struct{}{}:
			case <-loopCtx.Done():
				return // shutting down while waiting for a parallel slot
			}
			defer func() { <-e.sem }()
			e.runJob(runCtx, job)
		})
		return
	}

	select {
	case e.queue <- job:
	default:
		e.release(job.ID)
		e.emitError(job, phaseDispatch, errors.New("scheduling: sequential queue full"))
	}
}

// sequentialWorker is the sequential pool: one goroutine draining the queue
// in the order the source reported the jobs, so serialised jobs never
// overlap each other.
func (e *Engine) sequentialWorker(loopCtx, runCtx context.Context) {
	defer e.wg.Done()
	for {
		select {
		case <-loopCtx.Done():
			return
		case job := <-e.queue:
			e.runJob(runCtx, job)
			e.release(job.ID)
		}
	}
}

// runJob executes one run under the configured timeout, recovering panics so
// a job that blows up takes nothing else with it.
func (e *Engine) runJob(ctx context.Context, job Job) {
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.JobTimeout)
	defer cancel()

	start := e.cfg.Clock.Now()
	err := e.safeRun(runCtx, job)
	elapsed := e.cfg.Clock.Now().Sub(start)
	if err != nil {
		e.emitError(job, phaseRun, err)
		return
	}
	e.emit(KindJobRun, job, map[string]any{
		"job":         job.ID,
		"pool":        string(job.Pool.resolve()),
		"duration_ms": elapsed.Milliseconds(),
	})
}

func (e *Engine) safeRun(ctx context.Context, job Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("scheduling: job %q panicked: %v", job.ID, p)
		}
	}()
	return e.runner.Run(ctx, job)
}

// claim marks a job as queued-or-running, reporting false if it already was.
func (e *Engine) claim(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, busy := e.active[id]; busy {
		return false
	}
	e.active[id] = struct{}{}
	return true
}

func (e *Engine) release(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.active, id)
}

func (e *Engine) emitError(job Job, phase string, err error) {
	e.emit(KindJobError, job, map[string]any{
		"job":   job.ID,
		"pool":  string(job.Pool),
		"phase": phase,
		"err":   err.Error(),
	})
}

func (e *Engine) emit(kind string, job Job, data map[string]any) {
	if e.cfg.Events == nil {
		return
	}
	e.cfg.Events.Emit(kind, job.Detail, data)
}
