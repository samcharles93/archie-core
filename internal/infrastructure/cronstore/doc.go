// Package cronstore is the file-backed job store that backs the scheduling
// ticker engine. It is infrastructure: it owns the on-disk layout, the
// cross-process lock, and the version-stamped JSON schema. It does not own
// timing semantics — those live in internal/domain/scheduling.
//
// Two surfaces:
//
//   - scheduling.JobSource: Due(ctx, now) — what the engine consumes.
//   - CRUD on JobSpec — what the CLI and dashboard consume.
//
// Both share the same Store value, the same lock, and the same on-disk
// file. The engine applies no schedule arithmetic of its own; whatever
// "due" means is the store's decision, including future kinds the store
// grows into.
//
// Concurrency model: every read and write acquires an OS-level flock on a
// sibling lock file, so a CLI edit and a daemon writer cannot tear the file.
// Conflict resolution is last-writer-wins: the second writer overwrites the
// first's whole document. The lock makes that serial rather than racy.
// In-process, two Store values for the same path are serialised by a
// package-level mutex keyed by lock-file path — flock is per file
// description on Linux and would not block them otherwise.
//
// File format (versioned; bump schemaVersion on ANY shape change
// including additive ones, since DisallowUnknownFields rejects them):
//
//	{
//	  "schema_version": 1,
//	  "jobs": [ JobSpec, ... ]
//	}
//
// JobSpec carries the engine-facing fields it always needed (ID, Pool,
// Detail) plus everything the store computes for it (NextRun, LastRun,
// Created, Updated). Merge semantics on Update mean a nil field in a Patch
// leaves the corresponding persisted field untouched.
package cronstore
