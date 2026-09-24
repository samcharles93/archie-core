# One source for the dashboard's server vocabularies

**Status:** Finalised

## Problem

The dashboard kept its own copies of two vocabularies the daemon owns:

- the changed-file statuses (`added`, `modified`, `deleted`, `renamed`,
  `typechange`), which are the `Change*` constants in
  `internal/domain/workflow/task`;
- the schema stamp on a `config_captured` payload, `events.ConfigCapturedSchema`.

Nothing derived one side from the other, while `/api/task-meta` already served
the status and action vocabularies for exactly this reason.

## Decision

`GET /api/task-meta` also serves `change_statuses` (id and label) and
`config_schema`. `buildTaskMeta` in `internal/webui/api_task_meta.go` takes the
ids from the `Change*` constants and the stamp from `events.ConfigCapturedSchema`.

The dashboard renders `ui/src/lib/task-meta-snapshot.ts` until the served
catalogue arrives, or when archied is unreachable. `loadTaskMeta` replaces only
the keys the payload carries, so a server without the new keys leaves the
snapshot in place. `changeStatusLabel` and `configSchema` in
`ui/src/lib/task-meta.ts` are the only readers; an unknown status renders as
its id.

## Verification

- `TestTaskMetaChangeStatusesAreDeliberate` fails when a `Change*` constant is
  added without a label.

A rename on one side therefore fails a test on the other.

## Rejected

Deleting `/api/task-meta` and accepting hand-copied constants. The endpoint is
live, and deleting it would still leave the two copies this document removes.
