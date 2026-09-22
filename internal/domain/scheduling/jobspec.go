package scheduling

import (
	"errors"
	"time"
)

// Kind values for JobSpec.Kind: the discriminator that decides which delivery
// runner handles a job.
//
// They live beside the field that carries them because they are part of the
// schedules vocabulary an operator writes into the schedules document, a strict
// decoder reads back, and a build that never heard of a kind must reject loudly
// rather than deliver the wrong way. Which runner serves a kind is not this
// package's business: the router in internal/infrastructure/crondelivery owns
// that mapping, which is why an unrecognised kind is stored verbatim rather
// than refused at write time.
const (
	// KindChat delivers the job's Payload.Text as a chat message through the
	// deployment's channel. It is the default: an empty Kind means KindChat,
	// so every job written before this field existed keeps delivering the
	// way it did when it was created.
	KindChat = "chat"

	// KindWorkflow submits the job as a unit of work through the work-intake
	// path, so the existing task lifecycle owns scheduling, retries, gates
	// and PR opening.
	KindWorkflow = "workflow"
)

// JobSpec is one scheduled job as the schedules document persists it, plus
// the bookkeeping fields the scheduler computes (NextRun, LastRun, Created,
// Updated). The wire shape is the public API; the scheduling.Job returned
// to the engine is a stripped subset.
type JobSpec struct {
	// ID is the unique identifier. Required, non-empty, and the merge
	// key for run recording.
	ID string `json:"id"`

	// Pool is "parallel" or "sequential". Empty means sequential — the
	// safer default, matching Pool.resolve().
	Pool string `json:"pool,omitempty"`

	// Detail is a human-readable label rendered on events.
	Detail string `json:"detail,omitempty"`

	// Kind selects which delivery runner handles this job. Empty means
	// KindChat, so a job written before this field existed keeps
	// delivering as a chat message. This package does not interpret the
	// value: an unrecognised kind is stored verbatim and refused by the
	// delivery router, which is the one place the mapping from kind to
	// runner lives.
	Kind string `json:"kind,omitempty"`

	// Schedule decides when this job is due. See schedule.go for the
	// supported kinds and their arithmetic.
	Schedule Schedule `json:"schedule"`

	// Target and Payload describe where the job's output goes and what it
	// carries. They live on the wire so jobs created ahead of the delivery
	// runners that read them do not need a shape migration.
	Target  Target  `json:"target"`
	Payload Payload `json:"payload"`

	// NextRun is the absolute moment the job will next fire. Computed
	// at write time so the engine's hot path is a single comparison.
	NextRun time.Time `json:"next_run"`

	// LastRun is the last run time reported by the run-recording step,
	// nil if the job has never run. The schedule does not know whether
	// the run succeeded — that judgement belongs to the runner.
	LastRun *time.Time `json:"last_run,omitempty"`

	// Created and Updated are audit metadata written by the document's
	// owner; the engine does not read them.
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Target identifies where the job's output goes. Empty Target is valid —
// the delivery runner chooses a default.
type Target struct {
	// ChatID is the chat channel id (Telegram chat id, email address,
	// webhook URL — depends on the channel). Empty defers to a default
	// chosen by the delivery runner.
	ChatID string `json:"chat_id,omitempty"`
}

// Payload is the content sent at run time.
type Payload struct {
	// Text is the literal message body. Templating is a future concern
	// and is deliberately absent here.
	Text string `json:"text,omitempty"`
}

// Sentinels the schedule vocabulary and its callers match with errors.Is.
var (
	// ErrJobNotFound is returned by a job source when the requested id
	// is absent from its document.
	ErrJobNotFound = errors.New("scheduling: job not found")

	// ErrInvalidSpec is returned when a JobSpec or Schedule fails
	// structural validation (empty id, unknown schedule kind,
	// non-positive interval).
	ErrInvalidSpec = errors.New("scheduling: invalid job spec")

	// ErrScheduleUnsupported is returned when a job's schedule kind
	// cannot compute its next run time. Interval arithmetic is fully
	// implemented; cron and once return this so callers can surface a
	// clear error rather than silently advancing.
	ErrScheduleUnsupported = errors.New("scheduling: schedule kind not implemented")
)
