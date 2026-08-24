// Package scheduling is the ticker engine: the timing half of archied's
// cron/scheduling capability (epic archie-core-1786637496320-249-be6d9a20).
// It owns one job and one job only — deciding *when* a scheduled job runs,
// and with what concurrency — and nothing about where jobs are stored, how
// they are authored, or how their output is delivered.
//
// The split is deliberate. A job store (file-backed, cross-process locked)
// and a delivery router are separate issues in that epic; this package
// declares the two contracts it needs from them (JobSource, Runner) and
// names no implementation. That is what lets the timing behaviour — pool
// semantics, overlap suppression, per-run timeouts, bounded shutdown — be
// tested against a fake clock with no store, no NATS and no model.
package scheduling

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Pool is how a job's runs are dispatched relative to other jobs.
type Pool string

const (
	// PoolParallel runs the job concurrently with every other job, bounded
	// only by the engine's MaxParallel cap.
	PoolParallel Pool = "parallel"
	// PoolSequential runs the job on a single shared worker, one at a time,
	// in the order the source returned them. Use it for jobs that contend
	// for a resource that tolerates no concurrency.
	PoolSequential Pool = "sequential"
)

// Validate rejects an unknown pool. The empty pool is valid and means
// PoolSequential: an unlabelled job is assumed to want the safer of the two,
// because wrongly serialising a job costs latency while wrongly parallelising
// one costs correctness.
func (p Pool) Validate() error {
	switch p {
	case "", PoolParallel, PoolSequential:
		return nil
	default:
		return fmt.Errorf("scheduling: unknown pool %q", string(p))
	}
}

// resolve maps the empty pool onto its documented default.
func (p Pool) resolve() Pool {
	if p == "" {
		return PoolSequential
	}
	return p
}

// Job is one due unit of scheduled work as handed to the engine. It carries
// identity and dispatch policy only: the schedule itself (cron expression,
// next-run time, catch-up policy) belongs to the job store behind JobSource,
// which is why the engine never sees one.
type Job struct {
	// ID identifies the job across ticks. It is the overlap key: while a
	// run with this ID is queued or in flight, later ticks that report the
	// same ID are skipped rather than stacked.
	ID string
	// Pool selects the dispatch pool. Empty means PoolSequential.
	Pool Pool
	// Detail is a human-readable label recorded on emitted events so a run
	// stays attributable without the engine knowing what the job does.
	Detail string
}

// Validate checks the fields the engine relies on.
func (j Job) Validate() error {
	if strings.TrimSpace(j.ID) == "" {
		return errors.New("scheduling: job id must be non-empty")
	}
	return j.Pool.Validate()
}

// JobSource yields the jobs eligible to run at a given instant. The engine
// calls Due once per tick and treats it as authoritative: it applies no
// schedule arithmetic of its own, so a source is free to implement cron
// expressions, fixed intervals or catch-up windows without the engine
// changing.
type JobSource interface {
	Due(ctx context.Context, now time.Time) ([]Job, error)
}

// Runner executes one due job. Implementations must respect ctx: the engine
// bounds every run with JobTimeout and cancels on shutdown, but a Runner that
// ignores cancellation can still delay Stop up to Stop's own deadline.
type Runner interface {
	Run(ctx context.Context, job Job) error
}

// Clock is injectable time: Now for timestamps and After for the tick sleep.
// Loop timing is tested against a fake clock, never wall-clock sleeps.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Sink is the narrow event contract the engine needs. It matches
// events.Bus's emitter shape without importing it — the domain declares what
// it needs and app wires the implementation in.
type Sink interface {
	Emit(kind, detail string, data map[string]any)
}
