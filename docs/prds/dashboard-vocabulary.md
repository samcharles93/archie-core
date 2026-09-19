# One source for the dashboard's server vocabularies

**Status:** Settled design, landing with this change set (issue #865).
**Date:** 2026-09-19
**Compounds with:** `docs/prds/task-run-detail.md` (where the two hand-copied
vocabularies arrived), `docs/prds/config-schema.md` (the same
hand-authored-catalog trade-off, with the same guard test).

## The problem

Two dashboard modules kept a hand-copied copy of a server vocabulary:

- `ui/src/tasks/changed-files.jsx` `FILE_STATUS` mirrored the five `Change*`
  strings in `internal/domain/workflow/task/changes.go`.
- `ui/src/tasks/attempt-config.jsx` `CONFIG_SCHEMA` mirrored
  `events.ConfigCapturedSchema` in `internal/events/events.go`.

Each side was pinned by its own literal-value test, so a one-sided rename
failed *a* test — but nothing derived one side from the other, and both
`internal/taskstate`'s own `StatusMeta` doc and the dashboard's task-meta
module stated the opposite convention: `/api/task-meta` serves the vocabulary
so the frontend never keeps a hand-synced copy. That endpoint was dead,
though: `loadTaskMeta()` was exported with no callers, so the dashboard always
rendered its hardcoded snapshot. The tree carried two competing stories.

## Decision

Serve both vocabularies through `/api/task-meta` and revive `loadTaskMeta()`,
wiring it at boot.

`buildTaskMeta` (internal/webui/api_task_meta.go) now returns `statuses`,
`actions`, `change_statuses` and `config_schema`, with the change-status IDs
taken from the `task.Change*` constants and the schema stamp from
`events.ConfigCapturedSchema` — no retyped strings on the Go side. The
dashboard's snapshot in `ui/src/base/task-meta.jsx` remains the only literal
copy in the UI and is what first paint renders when archied is unreachable;
`loadTaskMeta()` replaces it with the served catalog and `main.jsx` re-renders
the mounted route in place (same key, no remount, no `archie:teardown`) so the
upgrade does not disturb page state. `applyTaskMeta` replaces only the keys the
payload carries, so a server that predates `change_statuses`/`config_schema`
leaves the snapshot in place.

The cross-language pin replaces the two per-side literal tests: the Go handler
must serve `internal/webui/testdata/task_meta.json` byte-for-byte
(`TestTaskMetaPayloadMatchesFixture`, regenerated with `-update`), and the
browser snapshot must deep-equal that same fixture before any load
(`ui/test/task-meta-catalogue.test.js`). A one-sided rename of any status,
action, change status or the schema now fails on the side that was not updated.

## The option rejected

Deleting `/api/task-meta` and accepting hand-copied constants as the
convention. That endpoint is live and has a real consumer (the Configuration
page's "Work lifecycle" card), it is served by both `archied` and `archie-ui`,
and `internal/taskstate` documents the served-catalog convention deliberately.
Deleting it would remove a shipped feature and contradict that rationale while
*still* leaving `FILE_STATUS`/`CONFIG_SCHEMA` hand-copied — the defect the
issue is about. Serving the two vocabularies costs one field, one boot call and
an in-place re-render; nothing is removed.

## Not fixed here

The `archie:teardown` event `show()` dispatches is inert: the listener is on
`window` (`ui/src/chat/chat.jsx`) while the event is dispatched non-bubbling on
the outlet's first child, and pages release their streams through effect
cleanup on unmount. This change avoids depending on it rather than repairing it
— that is a separate leak/behaviour change.
