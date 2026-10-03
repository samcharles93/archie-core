// Package crondelivery delivers due scheduled jobs: ChatCourier sends a chat
// message, WorkflowTask submits work, and Router picks one per job kind.
package crondelivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// SpecLookup resolves a job id to its stored spec.
type SpecLookup interface {
	// Get returns the job and whether it was found; the error is reserved
	// for I/O failures, matching scheduleResourceStore.Get.
	Get(ctx context.Context, id string) (scheduling.JobSpec, bool, error)
}

// RunRecorder advances a job's schedule after a successful run.
type RunRecorder interface {
	// MarkRun records that id ran at runAt and advances NextRun to the
	// schedule's next firing.
	MarkRun(ctx context.Context, id string, runAt time.Time) error
}

// RouterStore is what the Router needs from persistence: resolve a job id to
// its spec, and record the run once it succeeds. The per-kind runners still
// take the narrower SpecLookup -- they dispatch one already-hydrated job and
// have no business moving its schedule.
type RouterStore interface {
	SpecLookup
	RunRecorder
}

// Courier sends one chat message. It must honour ctx.
type Courier func(ctx context.Context, chatID, text string) error

// TaskSubmitter submits one unit of work.
type TaskSubmitter interface {
	Submit(ctx context.Context, identity, title, body string) error
}

// errSpecMissing reports a job the store does not know about. It is a run
// failure rather than a dispatch refusal: the source reported this job due, so
// failing to hydrate it is a real inconsistency, not a misconfiguration.
var errSpecMissing = errors.New("crondelivery: job spec not found")

// hydrate loads the job's persisted spec, wrapping both a store error and the
// not-found case so a caller can match on one sentinel.
func hydrate(ctx context.Context, specs SpecLookup, job scheduling.Job) (scheduling.JobSpec, error) {
	spec, ok, err := specs.Get(ctx, job.ID)
	if err != nil {
		return scheduling.JobSpec{}, fmt.Errorf("crondelivery: load job %q: %w", job.ID, err)
	}
	if !ok {
		return scheduling.JobSpec{}, fmt.Errorf("%w: id %q", errSpecMissing, job.ID)
	}
	return spec, nil
}

// Compile-time check that the production store satisfies the contract. It
// lives in internal/app/archied, which imports this package, so the assertion
// is made there (scheduling.go) rather than here, where it would be an import
// cycle.
