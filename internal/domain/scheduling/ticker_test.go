package scheduling

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- fakes --------------------------------------------------------------------

// fakeClock is the engine's injectable clock: Now plus clock-driven timers
// that fire when Advance moves past their deadline. Tick timing is tested
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

// pending reports how many timers are waiting; the tick loop registers
// exactly one at a time, so it is the signal that the loop is back asleep.
func (fc *fakeClock) pending() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.timers)
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

// sourceFunc adapts a function to JobSource.
type sourceFunc func(ctx context.Context, now time.Time) ([]Job, error)

func (f sourceFunc) Due(ctx context.Context, now time.Time) ([]Job, error) { return f(ctx, now) }

// staticSource returns the same jobs every tick and counts the calls.
type staticSource struct {
	jobs  []Job
	mu    sync.Mutex
	calls int
}

func (s *staticSource) Due(context.Context, time.Time) ([]Job, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.jobs, nil
}

func (s *staticSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// runnerFunc adapts a function to Runner.
type runnerFunc func(ctx context.Context, job Job) error

func (f runnerFunc) Run(ctx context.Context, job Job) error { return f(ctx, job) }

// recordingRunner records every run in order and optionally blocks on a gate
// keyed by job ID, so a test can hold a run in flight deterministically.
type recordingRunner struct {
	mu      sync.Mutex
	started []string
	done    []string
	inFlt   int
	maxFlt  int
	gates   map[string]chan struct{}
	err     error
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{gates: map[string]chan struct{}{}}
}

// gate installs a release channel for a job; runs of that job block until
// release is called.
func (r *recordingRunner) gate(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gates[id] = make(chan struct{})
}

func (r *recordingRunner) release(id string) {
	r.mu.Lock()
	ch := r.gates[id]
	delete(r.gates, id)
	r.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (r *recordingRunner) Run(ctx context.Context, job Job) error {
	r.mu.Lock()
	r.started = append(r.started, job.ID)
	r.inFlt++
	if r.inFlt > r.maxFlt {
		r.maxFlt = r.inFlt
	}
	gate := r.gates[job.ID]
	err := r.err
	r.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}

	r.mu.Lock()
	r.inFlt--
	r.done = append(r.done, job.ID)
	r.mu.Unlock()
	return err
}

func (r *recordingRunner) startedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...)
}

func (r *recordingRunner) doneIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.done...)
}

func (r *recordingRunner) peakInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxFlt
}

// recordingSink collects emitted events.
type recordingSink struct {
	mu     sync.Mutex
	events []sinkEvent
}

type sinkEvent struct {
	kind   string
	detail string
	data   map[string]any
}

func (s *recordingSink) Emit(kind, detail string, data map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, sinkEvent{kind: kind, detail: detail, data: data})
}

func (s *recordingSink) countOf(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.kind == kind {
			n++
		}
	}
	return n
}

func (s *recordingSink) firstOf(kind string) (sinkEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.kind == kind {
			return e, true
		}
	}
	return sinkEvent{}, false
}

// --- helpers ------------------------------------------------------------------

const (
	testInterval = time.Minute
	testTimeout  = 2 * time.Second
	pollGap      = time.Millisecond
)

func testStart() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(pollGap)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// tick advances the fake clock one interval and waits for the loop to arm its
// next timer, which happens only after the tick's dispatch has been decided.
func tick(t *testing.T, fc *fakeClock, e *Engine) {
	t.Helper()
	waitFor(t, "loop to arm its tick timer", func() bool { return fc.pending() > 0 })
	fc.Advance(testInterval)
	waitFor(t, "loop to re-arm after tick", func() bool { return fc.pending() > 0 })
	_ = e
}

// startEngine builds and starts an engine, registering Stop as cleanup.
func startEngine(t *testing.T, src JobSource, runner Runner, cfg EngineConfig) *Engine {
	t.Helper()
	e, err := NewEngine(src, runner, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_ = e.Stop(ctx)
	})
	return e
}

func countOccurrences(ids []string, want string) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}

// --- construction -------------------------------------------------------------

func TestNewEngineValidatesConfig(t *testing.T) {
	src := &staticSource{}
	runner := newRecordingRunner()

	tests := []struct {
		name    string
		source  JobSource
		runner  Runner
		cfg     EngineConfig
		wantErr bool
	}{
		{name: "nil source", source: nil, runner: runner, cfg: EngineConfig{}, wantErr: true},
		{name: "nil runner", source: src, runner: nil, cfg: EngineConfig{}, wantErr: true},
		{name: "negative interval", source: src, runner: runner, cfg: EngineConfig{Interval: -testInterval}, wantErr: true},
		{name: "negative max parallel", source: src, runner: runner, cfg: EngineConfig{MaxParallel: -1}, wantErr: true},
		{name: "negative job timeout", source: src, runner: runner, cfg: EngineConfig{JobTimeout: -testInterval}, wantErr: true},
		{name: "zero values are defaulted", source: src, runner: runner, cfg: EngineConfig{}, wantErr: false},
		{
			name: "fully specified", source: src, runner: runner,
			cfg: EngineConfig{Interval: testInterval, MaxParallel: 2, JobTimeout: testInterval},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := NewEngine(tc.source, tc.runner, tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got engine %v", e)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if e == nil {
				t.Fatal("expected engine, got nil")
			}
		})
	}
}

func TestNewEngineAppliesDefaults(t *testing.T) {
	e, err := NewEngine(&staticSource{}, newRecordingRunner(), EngineConfig{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if got := e.Interval(); got != DefaultInterval {
		t.Errorf("interval = %v, want %v", got, DefaultInterval)
	}
	if got := e.MaxParallel(); got != DefaultMaxParallel {
		t.Errorf("max parallel = %d, want %d", got, DefaultMaxParallel)
	}
	if got := e.JobTimeout(); got != DefaultJobTimeout {
		t.Errorf("job timeout = %v, want %v", got, DefaultJobTimeout)
	}
}

func TestEngineHonoursConfiguredInterval(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, testInterval, time.Hour} {
		t.Run(interval.String(), func(t *testing.T) {
			fc := newFakeClock(testStart())
			src := &staticSource{}
			e := startEngine(t, src, newRecordingRunner(), EngineConfig{Interval: interval, Clock: fc})

			waitFor(t, "first tick timer", func() bool { return fc.pending() > 0 })
			// One nanosecond short of the interval must not tick.
			fc.Advance(interval - time.Nanosecond)
			time.Sleep(10 * pollGap)
			if got := src.callCount(); got != 0 {
				t.Fatalf("source called %d times before the interval elapsed", got)
			}
			fc.Advance(time.Nanosecond)
			waitFor(t, "source poll", func() bool { return src.callCount() >= 1 })
			if got := e.Interval(); got != interval {
				t.Errorf("Interval() = %v, want %v", got, interval)
			}
		})
	}
}

// --- dispatch -----------------------------------------------------------------

func TestEngineRunsDueJobs(t *testing.T) {
	fc := newFakeClock(testStart())
	jobs := []Job{{ID: "a", Pool: PoolParallel}, {ID: "b", Pool: PoolSequential}}
	src := &staticSource{jobs: jobs}
	runner := newRecordingRunner()
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc})

	tick(t, fc, e)
	waitFor(t, "both jobs to finish", func() bool { return len(runner.doneIDs()) == len(jobs) })

	for _, j := range jobs {
		if countOccurrences(runner.doneIDs(), j.ID) != 1 {
			t.Errorf("job %q ran %d times, want 1", j.ID, countOccurrences(runner.doneIDs(), j.ID))
		}
	}
}

func TestEngineParallelPoolRunsConcurrentlyUpToCap(t *testing.T) {
	const maxParallel = 3
	fc := newFakeClock(testStart())
	jobs := make([]Job, 0, maxParallel+2)
	runner := newRecordingRunner()
	for i := range cap(jobs) {
		id := string(rune('a' + i))
		jobs = append(jobs, Job{ID: id, Pool: PoolParallel})
		runner.gate(id)
	}
	src := &staticSource{jobs: jobs}
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, MaxParallel: maxParallel, Clock: fc})

	tick(t, fc, e)
	waitFor(t, "cap to be saturated", func() bool { return runner.peakInFlight() >= maxParallel })
	// Give any over-cap goroutine a chance to break the cap before asserting.
	time.Sleep(10 * pollGap)
	if got := runner.peakInFlight(); got != maxParallel {
		t.Fatalf("peak in-flight = %d, want %d", got, maxParallel)
	}
	for _, j := range jobs {
		runner.release(j.ID)
	}
	waitFor(t, "all jobs to finish", func() bool { return len(runner.doneIDs()) == len(jobs) })
}

func TestEngineSequentialPoolRunsOneAtATimeInOrder(t *testing.T) {
	fc := newFakeClock(testStart())
	jobs := []Job{{ID: "first"}, {ID: "second", Pool: PoolSequential}, {ID: "third", Pool: PoolSequential}}
	src := &staticSource{jobs: jobs}
	runner := newRecordingRunner()
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, MaxParallel: len(jobs), Clock: fc})

	tick(t, fc, e)
	waitFor(t, "sequential jobs to finish", func() bool { return len(runner.doneIDs()) == len(jobs) })

	if got := runner.peakInFlight(); got != 1 {
		t.Errorf("sequential peak in-flight = %d, want 1", got)
	}
	for i, id := range runner.startedIDs() {
		if id != jobs[i].ID {
			t.Errorf("run %d = %q, want %q (source order)", i, id, jobs[i].ID)
		}
	}
}

func TestEngineSequentialJobDoesNotBlockParallelJob(t *testing.T) {
	fc := newFakeClock(testStart())
	src := &staticSource{jobs: []Job{{ID: "slow-seq", Pool: PoolSequential}, {ID: "quick-par", Pool: PoolParallel}}}
	runner := newRecordingRunner()
	runner.gate("slow-seq")
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc})

	tick(t, fc, e)
	waitFor(t, "parallel job to finish while sequential is blocked", func() bool {
		return countOccurrences(runner.doneIDs(), "quick-par") == 1
	})
	runner.release("slow-seq")
	waitFor(t, "sequential job to finish", func() bool {
		return countOccurrences(runner.doneIDs(), "slow-seq") == 1
	})
}

func TestEngineSkipsJobAlreadyInFlight(t *testing.T) {
	for _, pool := range []Pool{PoolParallel, PoolSequential} {
		t.Run(string(pool), func(t *testing.T) {
			const id = "overlapping"
			fc := newFakeClock(testStart())
			src := &staticSource{jobs: []Job{{ID: id, Pool: pool}}}
			runner := newRecordingRunner()
			runner.gate(id)
			e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc})

			tick(t, fc, e)
			waitFor(t, "first run to start", func() bool { return len(runner.startedIDs()) == 1 })
			tick(t, fc, e)
			tick(t, fc, e)
			time.Sleep(10 * pollGap)
			if got := len(runner.startedIDs()); got != 1 {
				t.Fatalf("job started %d times while in flight, want 1", got)
			}
			runner.release(id)
			waitFor(t, "first run to finish", func() bool { return len(runner.doneIDs()) == 1 })

			// Once the run is done the job is eligible again.
			tick(t, fc, e)
			waitFor(t, "second run", func() bool { return len(runner.startedIDs()) == 2 })
		})
	}
}

func TestEngineRejectsInvalidJob(t *testing.T) {
	tests := []struct {
		name string
		job  Job
	}{
		{name: "empty id", job: Job{ID: "  ", Pool: PoolParallel}},
		{name: "unknown pool", job: Job{ID: "bad-pool", Pool: Pool("elsewhere")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeClock(testStart())
			sink := &recordingSink{}
			src := &staticSource{jobs: []Job{tc.job, {ID: "healthy", Pool: PoolParallel}}}
			runner := newRecordingRunner()
			e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc, Events: sink})

			tick(t, fc, e)
			waitFor(t, "healthy job to run", func() bool {
				return countOccurrences(runner.doneIDs(), "healthy") == 1
			})
			if got := len(runner.startedIDs()); got != 1 {
				t.Fatalf("started %v, want only the healthy job", runner.startedIDs())
			}
			if sink.countOf(KindJobError) == 0 {
				t.Error("expected an error event for the invalid job")
			}
		})
	}
}

// --- failure isolation --------------------------------------------------------

func TestEngineSurvivesSourceError(t *testing.T) {
	fc := newFakeClock(testStart())
	sink := &recordingSink{}
	var calls int
	var mu sync.Mutex
	wantErr := errors.New("store unavailable")
	src := sourceFunc(func(context.Context, time.Time) ([]Job, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return nil, wantErr
		}
		return []Job{{ID: "after-failure"}}, nil
	})
	runner := newRecordingRunner()
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc, Events: sink})

	tick(t, fc, e)
	waitFor(t, "error event", func() bool { return sink.countOf(KindJobError) == 1 })
	tick(t, fc, e)
	waitFor(t, "loop to keep ticking after a source error", func() bool {
		return countOccurrences(runner.doneIDs(), "after-failure") == 1
	})

	ev, ok := sink.firstOf(KindJobError)
	if !ok {
		t.Fatal("no error event recorded")
	}
	if got, _ := ev.data["phase"].(string); got != phaseSource {
		t.Errorf("error phase = %q, want %q", got, phaseSource)
	}
}

func TestEngineSurvivesJobPanicAndError(t *testing.T) {
	tests := []struct {
		name string
		run  func(job Job) error
	}{
		{name: "panic", run: func(Job) error { panic("boom") }},
		{name: "error", run: func(Job) error { return errors.New("job failed") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeClock(testStart())
			sink := &recordingSink{}
			var mu sync.Mutex
			var runs int
			src := &staticSource{jobs: []Job{{ID: "unstable"}}}
			runner := runnerFunc(func(_ context.Context, job Job) error {
				mu.Lock()
				runs++
				mu.Unlock()
				return tc.run(job)
			})
			e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc, Events: sink})

			tick(t, fc, e)
			waitFor(t, "first run", func() bool { mu.Lock(); defer mu.Unlock(); return runs == 1 })
			waitFor(t, "error event", func() bool { return sink.countOf(KindJobError) == 1 })
			// The loop survives: the same job is dispatched again next tick.
			tick(t, fc, e)
			waitFor(t, "second run", func() bool { mu.Lock(); defer mu.Unlock(); return runs == 2 })

			ev, _ := sink.firstOf(KindJobError)
			if got, _ := ev.data["phase"].(string); got != phaseRun {
				t.Errorf("error phase = %q, want %q", got, phaseRun)
			}
		})
	}
}

func TestEngineBoundsRunWithJobTimeout(t *testing.T) {
	fc := newFakeClock(testStart())
	sink := &recordingSink{}
	src := &staticSource{jobs: []Job{{ID: "hangs"}}}
	deadlines := make(chan error, 1)
	runner := runnerFunc(func(ctx context.Context, _ Job) error {
		<-ctx.Done()
		deadlines <- ctx.Err()
		return ctx.Err()
	})
	// A real (short) timeout: the run's deadline is wall-clock, because a
	// Runner's ctx must be honoured by code that knows nothing of our clock.
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, JobTimeout: 20 * time.Millisecond, Clock: fc, Events: sink})

	tick(t, fc, e)
	select {
	case err := <-deadlines:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("run ctx error = %v, want DeadlineExceeded", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("run was never cancelled by the job timeout")
	}
	waitFor(t, "timeout to be reported", func() bool { return sink.countOf(KindJobError) == 1 })
}

// --- lifecycle ----------------------------------------------------------------

func TestEngineStartTwiceIsAnError(t *testing.T) {
	fc := newFakeClock(testStart())
	e := startEngine(t, &staticSource{}, newRecordingRunner(), EngineConfig{Interval: testInterval, Clock: fc})
	if err := e.Start(context.Background()); err == nil {
		t.Fatal("expected an error starting an already-started engine")
	}
}

func TestEngineRestartAfterStopIsRefused(t *testing.T) {
	fc := newFakeClock(testStart())
	e, err := NewEngine(&staticSource{}, newRecordingRunner(), EngineConfig{Interval: testInterval, Clock: fc})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := e.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := e.Start(context.Background()); err == nil {
		t.Fatal("expected an error restarting a stopped engine")
	}
}

func TestEngineStopWithoutStartIsNoOp(t *testing.T) {
	e, err := NewEngine(&staticSource{}, newRecordingRunner(), EngineConfig{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := e.Stop(ctx); err != nil {
		t.Fatalf("Stop before Start = %v, want nil", err)
	}
}

func TestEngineStopWaitsForInFlightRuns(t *testing.T) {
	fc := newFakeClock(testStart())
	src := &staticSource{jobs: []Job{{ID: "par", Pool: PoolParallel}, {ID: "seq", Pool: PoolSequential}}}
	runner := newRecordingRunner()
	runner.gate("par")
	runner.gate("seq")
	e, err := NewEngine(src, runner, EngineConfig{Interval: testInterval, Clock: fc})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tick(t, fc, e)
	waitFor(t, "both runs to start", func() bool { return len(runner.startedIDs()) == 2 })

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		stopped <- e.Stop(ctx)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned while runs were still in flight")
	case <-time.After(20 * pollGap):
	}

	runner.release("par")
	runner.release("seq")
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Stop did not return after runs finished")
	}
	if got := len(runner.doneIDs()); got != 2 {
		t.Fatalf("finished runs = %d, want 2", got)
	}
}

func TestEngineStopIsBoundedByContext(t *testing.T) {
	fc := newFakeClock(testStart())
	src := &staticSource{jobs: []Job{{ID: "uninterruptible"}}}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	runner := runnerFunc(func(context.Context, Job) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // deliberately ignores cancellation
		return nil
	})
	e, err := NewEngine(src, runner, EngineConfig{Interval: testInterval, Clock: fc})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer close(release)

	tick(t, fc, e)
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*pollGap)
	defer cancel()
	if err := e.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v, want a deadline-exceeded error", err)
	}
}

func TestEngineStopStopsTicking(t *testing.T) {
	fc := newFakeClock(testStart())
	src := &staticSource{jobs: []Job{{ID: "job"}}}
	runner := newRecordingRunner()
	e, err := NewEngine(src, runner, EngineConfig{Interval: testInterval, Clock: fc})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tick(t, fc, e)
	waitFor(t, "first run", func() bool { return len(runner.doneIDs()) == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := e.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	before := src.callCount()
	fc.Advance(testInterval * 10)
	time.Sleep(20 * pollGap)
	if got := src.callCount(); got != before {
		t.Fatalf("source polled %d times after Stop (was %d)", got, before)
	}
}

// --- events -------------------------------------------------------------------

func TestEngineEmitsRunEvents(t *testing.T) {
	fc := newFakeClock(testStart())
	sink := &recordingSink{}
	const detail = "daily status summary"
	src := &staticSource{jobs: []Job{{ID: "summary", Pool: PoolParallel, Detail: detail}}}
	e := startEngine(t, src, newRecordingRunner(), EngineConfig{Interval: testInterval, Clock: fc, Events: sink})

	tick(t, fc, e)
	waitFor(t, "run event", func() bool { return sink.countOf(KindJobRun) == 1 })

	ev, _ := sink.firstOf(KindJobRun)
	if ev.detail != detail {
		t.Errorf("event detail = %q, want %q", ev.detail, detail)
	}
	if got, _ := ev.data["job"].(string); got != "summary" {
		t.Errorf("event job = %q, want %q", got, "summary")
	}
	if got, _ := ev.data["pool"].(string); got != string(PoolParallel) {
		t.Errorf("event pool = %q, want %q", got, PoolParallel)
	}
}

func TestEngineWithoutSinkDoesNotPanic(t *testing.T) {
	fc := newFakeClock(testStart())
	src := &staticSource{jobs: []Job{{ID: "bad-pool", Pool: Pool("nowhere")}, {ID: "fine"}}}
	runner := newRecordingRunner()
	e := startEngine(t, src, runner, EngineConfig{Interval: testInterval, Clock: fc})

	tick(t, fc, e)
	waitFor(t, "valid job to run", func() bool { return countOccurrences(runner.doneIDs(), "fine") == 1 })
}

// --- contract -----------------------------------------------------------------

func TestPoolValidate(t *testing.T) {
	tests := []struct {
		pool    Pool
		wantErr bool
	}{
		{pool: PoolParallel},
		{pool: PoolSequential},
		{pool: ""},
		{pool: Pool("PARALLEL"), wantErr: true},
		{pool: Pool("other"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(string(tc.pool), func(t *testing.T) {
			if err := tc.pool.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%q) = %v, wantErr %v", tc.pool, err, tc.wantErr)
			}
		})
	}
}

func TestPoolResolveDefaultsToSequential(t *testing.T) {
	tests := []struct {
		pool Pool
		want Pool
	}{
		{pool: "", want: PoolSequential},
		{pool: PoolSequential, want: PoolSequential},
		{pool: PoolParallel, want: PoolParallel},
	}
	for _, tc := range tests {
		t.Run(string(tc.pool), func(t *testing.T) {
			if got := tc.pool.resolve(); got != tc.want {
				t.Fatalf("resolve(%q) = %q, want %q", tc.pool, got, tc.want)
			}
		})
	}
}

func TestJobValidate(t *testing.T) {
	tests := []struct {
		name    string
		job     Job
		wantErr bool
	}{
		{name: "valid", job: Job{ID: "a", Pool: PoolParallel}},
		{name: "valid default pool", job: Job{ID: "a"}},
		{name: "empty id", job: Job{ID: ""}, wantErr: true},
		{name: "blank id", job: Job{ID: "\t "}, wantErr: true},
		{name: "unknown pool", job: Job{ID: "a", Pool: Pool("x")}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.job.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
