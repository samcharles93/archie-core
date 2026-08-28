package cronstore

import "errors"

// Sentinel errors callers can match with errors.Is.
var (
	// ErrUnsupportedSchema is returned by Open when the persisted file
	// declares a schema version this build does not understand. The
	// store treats that as an explicit refusal to read: a forward
	// version may have shape we cannot safely render back out, and
	// silently downgrading would lose data.
	ErrUnsupportedSchema = errors.New("cronstore: unsupported schema version")

	// ErrCorruptFile is returned by Open when the file exists but does
	// not decode as a valid envelope: truncated, garbage, or fields of
	// the wrong shape. The store never panics on bad input, so the
	// engine can surface this as phase="source" KindJobError.
	ErrCorruptFile = errors.New("cronstore: corrupt file")

	// ErrJobExists is returned by Create when the requested id is
	// already present. Callers that want upsert semantics should
	// follow up with Update.
	ErrJobExists = errors.New("cronstore: job already exists")

	// ErrJobNotFound is returned by Get, Update and MarkRun when the
	// id is absent. Delete is the exception: it is idempotent.
	ErrJobNotFound = errors.New("cronstore: job not found")

	// ErrInvalidSpec is returned by Create and Update when the supplied
	// JobSpec / Patch fails the store's structural validation (empty id,
	// unknown schedule kind, non-positive interval).
	ErrInvalidSpec = errors.New("cronstore: invalid job spec")

	// ErrScheduleUnsupported is returned by MarkRun when the job's
	// schedule kind cannot compute its next run time. Slice 1 only
	// knows interval arithmetic; cron / once return this so callers
	// can surface a clear error rather than silently advancing.
	ErrScheduleUnsupported = errors.New("cronstore: schedule kind not implemented")
)
