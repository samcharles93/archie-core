// Package crondelivery is the delivery half of archied's cron/scheduling
// capability: it turns a due job into an effect.
//
// The domain's ticker engine (internal/domain/scheduling) decides *when* a job
// runs and hands over a scheduling.Job — identity and dispatch policy, nothing
// more. This package owns what happens next, and it is the only place that
// knows a job store exists. The engine sees one Runner (the Router); the
// deliveries it dispatches to are a mapping this package holds.
//
// Three surfaces:
//
//   - ChatCourier sends a job's payload text to a chat.
//   - WorkflowTask submits a job as a unit of work.
//   - Router picks between them per job kind, and is the single Runner the
//     engine is given.
//
// No implementation here talks to a channel, a broker or a forge. Each takes
// the effect it needs as an injected function or narrow interface, so which
// chat transport carries a message — and which intake path receives a task —
// stays the deployment's decision (the wiring slice composes the real ones),
// not the cron's. That is also what keeps this package testable without a
// network.
package crondelivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

// SpecLookup resolves a job id to its persisted spec.
//
// It is declared here (consumer-owned) rather than imported from cronstore as
// a concrete type so this package depends on the one method it uses.
// *cronstore.Store satisfies it as written, so a deployment passes the store
// straight in.
type SpecLookup interface {
	// Get returns the job and whether it was found; the error is reserved
	// for I/O failures, matching cronstore.Store.Get.
	Get(ctx context.Context, id string) (cronstore.JobSpec, bool, error)
}

// Courier sends one chat message. It is the whole outbound surface this
// package needs, and it is injected: the concrete channel client lives in the
// deployment, so an operator switching transports changes nothing here. That
// is how "the channel choice stays the operator's, not the cron's" is enforced
// — by construction, there is no channel to choose from here.
//
// An implementation must honour ctx: the engine bounds every run with a
// timeout and cancels on shutdown, and a courier that blocks through
// cancellation keeps the run in flight past the engine's own deadline.
type Courier func(ctx context.Context, chatID, text string) error

// TaskSubmitter submits one unit of work to the work-intake path.
//
// identity is the submitting job's id; title and body are the job's detail and
// payload. It is narrow on purpose: the concrete publisher builds the
// work-intake envelope, because the envelope carries owner/repo/number fields
// only the deployment can know. This package carries no forge vocabulary.
type TaskSubmitter interface {
	Submit(ctx context.Context, identity, title, body string) error
}

// errSpecMissing reports a job the store does not know about. It is a run
// failure rather than a dispatch refusal: the source reported this job due, so
// failing to hydrate it is a real inconsistency, not a misconfiguration.
var errSpecMissing = errors.New("crondelivery: job spec not found")

// hydrate loads the job's persisted spec, wrapping both a store error and the
// not-found case so a caller can match on one sentinel.
func hydrate(ctx context.Context, specs SpecLookup, job scheduling.Job) (cronstore.JobSpec, error) {
	spec, ok, err := specs.Get(ctx, job.ID)
	if err != nil {
		return cronstore.JobSpec{}, fmt.Errorf("crondelivery: load job %q: %w", job.ID, err)
	}
	if !ok {
		return cronstore.JobSpec{}, fmt.Errorf("%w: id %q", errSpecMissing, job.ID)
	}
	return spec, nil
}

// compile-time check: the store is the lookup a deployment hands in unchanged.
var _ SpecLookup = (*cronstore.Store)(nil)
