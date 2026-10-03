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

// phaseDispatch is the phase reported for a dispatch refusal; it matches the
// scheduling engine's value.
const phaseDispatch = "dispatch"

// Router runs a due job with the runner for its kind and records successful
// runs. An unknown kind is reported as a dispatch KindJobError and Run
// returns nil.
type Router struct {
	specs   RouterStore
	runners map[string]scheduling.Runner
	sink    scheduling.Sink
}

// NewRouter builds a router over specs and runners. A nil sink disables
// events. An empty kind is KindChat.
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

// recordRun advances the job's schedule after a successful run.
// ErrScheduleUnsupported (a one-shot) is ignored.
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
