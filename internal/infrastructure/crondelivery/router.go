package crondelivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
)

// phaseDispatch names the step that failed on a KindJobError the Router emits.
//
// It is restated as a literal rather than referenced, because the engine's
// equivalent constant is unexported (internal/domain/scheduling's
// phaseDispatch). "dispatch" is a wire value on the event stream and this
// package must match it, so it is pinned here by value.
const phaseDispatch = "dispatch"

// Router is the single Runner the engine is given. It resolves a due job's
// kind from its persisted spec and hands the job to that kind's runner,
// keeping the engine unaware that more than one delivery exists. Once a run
// has succeeded it also records the run, which is what moves a recurring job's
// NextRun.
//
// An unknown kind is refused, not failed. It is reported on the event stream
// as KindJobError with phase "dispatch" — naming the gate, so an operator can
// distinguish a misconfigured job from a broken one — and Run returns nil.
// Returning an error instead would have the engine emit it as a failed *run*
// (phase "run"), conflating the two, and would leave the job looking like it
// tried and broke on every tick.
type Router struct {
	specs   RouterStore
	runners map[string]scheduling.Runner
	sink    scheduling.Sink
}

// NewRouter builds the router over a spec lookup, its kind-to-runner mapping,
// and the sink its dispatch refusals are reported on. A nil sink disables
// emission — the router still refuses, it is just unobservable, the same
// tolerance the engine has for its own nil sink.
//
// An empty kind resolves to scheduling.KindChat, so a deployment only needs a
// mapping for the kinds it actually uses.
func NewRouter(specs RouterStore, runners map[string]scheduling.Runner, sink scheduling.Sink) (*Router, error) {
	if specs == nil {
		return nil, errors.New("crondelivery: spec lookup must not be nil")
	}
	if len(runners) == 0 {
		return nil, errors.New("crondelivery: at least one runner is required")
	}
	return &Router{specs: specs, runners: runners, sink: sink}, nil
}

// Run implements scheduling.Runner: hydrate, resolve the kind, dispatch.
func (r *Router) Run(ctx context.Context, job scheduling.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	spec, err := hydrate(ctx, r.specs, job)
	if err != nil {
		return err
	}

	kind := spec.Kind
	if kind == "" {
		kind = scheduling.KindChat
	}
	runner, ok := r.runners[kind]
	if !ok {
		r.refuse(job, kind)
		return nil
	}
	if err := runner.Run(ctx, job); err != nil {
		return err
	}
	return r.recordRun(ctx, job)
}

// recordRun advances the job's schedule after a successful run. Without it the
// store has no record that the job ran, so its NextRun stays in the past and
// Due reports the job again on every tick: a job scheduled hourly fires once
// per tick instead.
//
// A schedule kind with no recurring next run -- a one-shot -- reports
// scheduling.ErrScheduleUnsupported, which is the job's definition rather than a
// run failure, so it is swallowed. Anything else is returned: the run happened
// but the bookkeeping did not, and hiding that would leave the job silently
// re-firing.
func (r *Router) recordRun(ctx context.Context, job scheduling.Job) error {
	if err := r.specs.MarkRun(ctx, job.ID, time.Now()); err != nil && !errors.Is(err, scheduling.ErrScheduleUnsupported) {
		return fmt.Errorf("crondelivery: record run of job %q: %w", job.ID, err)
	}
	return nil
}

// refuse reports a job whose kind has no runner. The data map matches the
// engine's own error shape — {"job", "pool", "phase", "err"} — so one consumer
// renders a dispatch refusal and a failed run without a special case, and the
// detail is the job's human-readable label, exactly as the engine sets it.
func (r *Router) refuse(job scheduling.Job, kind string) {
	if r.sink == nil {
		return
	}
	r.sink.Emit(events.KindJobError, job.Detail, map[string]any{
		"job":   job.ID,
		"pool":  string(job.Pool),
		"phase": phaseDispatch,
		// The kind is in the message, not a bare "unknown job": the
		// operator's next step is to add a runner mapping for it.
		"err": fmt.Sprintf("crondelivery: no runner registered for job kind %q",
			strings.TrimSpace(kind)),
	})
}

var _ scheduling.Runner = (*Router)(nil)
