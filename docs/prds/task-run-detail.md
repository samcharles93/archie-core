# Per-run task detail: attempts, stages, changes, config, debug

**Status:** Approved
**Authority:** the surface contract itself is
`docs/architecture/observability.md`.
**Date:** 2026-09-18
**Compounds with:** `docs/prds/task-logs-pagination.md` (a bounded read says
what it does not show), `docs/prds/status-health-surface.md` (no surface that
answers something already answered elsewhere).

## Decision

A task gets one detail page that answers "what did this run actually do?" per
**attempt**: an ordered stage rail, the change each commit/push produced, the
effective configuration the attempt ran under, the attempt's log, and the raw
record behind all of it.

The single organising idea is **attempt attribution**. The dashboard
renders a task's whole event stream as one flat timeline, so a retried task
reads as one long run with two of everything. Every new panel is scoped to one
attempt and says which one.

The second organising idea is **provenance, not re-derivation**. What a run
produced is captured at the producer, while the evidence exists, and persisted
durably. Nothing in this surface tries to reconstruct a past run from state
that has since moved on.

## The requirement set

| # | Requirement | Mechanism |
| --- | --- | --- |
| R1 | Per-attempt stage rail: stages with name, start, duration, terminal status and error, ordered within one attempt | `GET /api/tasks/{id}/attempts` reads the task's events and groups them by `attempt` |
| R2 | Events carry an attempt, migrated safely and carried on the wire | `events.attempt` column + presence-gated migrator arm; `Event.attempt = 11` |
| R3 | Changed files captured at commit/push, persisted per task, exposed, shown with repo/PR links | `changes_captured` durable event written by the commit/push steps; `GET /api/tasks/{id}/changes` |
| R4 | The effective configuration an attempt ran under, secrets redacted, shown | `config_captured` durable event written once per attempt; read from the events response the page already holds |
| R5 | Log pane filters by attempt, stage and level; absence stays distinct from an unreadable deployment | `logging.Query.Stage` + `ReadTaskLogRequest.stage = 9`; the pane keeps `disabled` and `found=false` apart |
| R6 | Retry is reachable from the run view and produces a new attempt shown as a new run | Existing retry action, labelled as a NEW run whose confirm states the previous attempt's commits are discarded |
| R7 | Raw JSON of the task record and its events for the selected attempt | `GET /api/tasks/{id}/debug` |
| R8 | Deep-linkable, reload-safe per-task detail page that keeps existing URLs working | `/tasks/{id}` route beside the existing `/tasks?task=N` and `/tasks?status=...` |
| R9 | Design polish: hierarchy, tokens, both themes, responsive rail, a11y, loading/empty/error states | UI lane; measured at 480px and 700px in both themes |
| R10 | Documentation of record | This document plus `docs/architecture/observability.md` |
| R11 | Red-first tests for every new behaviour, full gate, `task ui`, forced `go build ./...`, `go test -race` on touched packages | Per lane; the anti-cost-cutting requirement lives here |

## Attempt attribution: a column, not a guess

`events.Event.Attempt`, persisted by `internal/store` as the `events.attempt`
column (`internal/store/events.go`, `eventsSchema`), is a real column,
`NOT NULL DEFAULT 0`. It was chosen over segmenting the stream on `task_queued` /
`task_retried` because a fold would have to guess which side of a boundary an
event belongs to when the daemon restarts mid-attempt, when `RecoverStale`
requeues, or when two attempts interleave across a crash. A guess rendered as
structure is exactly the false fidelity this surface exists to avoid; a column
is durable, queryable and ordered by construction.

**`0` means "not attributable".** It is every row written before this change
plus the deliberately task-agnostic producers. It is not an attempt number and
is never rendered as one. The attempts endpoint reports the count of
unattributed events so the page can say so instead of presenting old history as
attempt 1.

## Migration against an existing `archie.db`

The store's migrator (`migrateTasks` in `internal/store/store.go`) already reads
`PRAGMA table_info` through `tableColumns` and applies a presence-gated
`ALTER TABLE ... ADD COLUMN` list. This change adds an `events` arm: a
`tableColumns(ctx, tx, "events")` read beside the other two, and the entry
`ALTER TABLE events ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0`.

Three consequences a future maintainer must not get wrong:

- `eventsSchema` is `CREATE TABLE IF NOT EXISTS` (`internal/store/events.go`),
  so on an existing database it is a **no-op**. The migrator arm is the only
  thing that adds the column there. This is why the red test builds a raw
  SQLite file with the pre-change `events` DDL and must fail before the arm
  exists: every temp-database test passes either way.
- **`user_version` stays 3.** The loop is presence-gated, not version-gated;
  bumping it would force an edit to the test that exists to prove a newer
  schema is refused.
- **No backfill, ever.** Existing rows keep `attempt = 0`. Inferring an attempt
  number for them would be fabrication.

## Changed files: captured at the producer

The measurement is taken inside the shared step library immediately after a
successful commit and after a successful push (`StageCommit` and
`StageCommitPush` in `internal/domain/workflow/steps.go`) — the one place
where the worktree, the branch, the base, the task and the store are all in
scope, and the only place that knows a push actually happened.

Both alternatives were rejected on evidence, not taste:

- **Reading the diff on demand** is impossible. The worktree is deleted on
  merge, close and no-PR terminal states (`internal/worktree.(*Manager).Cleanup`,
  called from the daemon's terminal paths) and a retry resets the branch onto
  its base (`internal/worktree.(*Manager).refresh` → `resetOnto`). There is
  nothing left to read.
- **Reading it from the forge** is impossible. The forge contract exposes
  no changed-file capability, and adding one would cost three implementations
  plus an RPC and would still fail for chat-sourced tasks with no PR.

Binding consequences:

- Capture **degrades, never parks**. A `Trees` implementation without the
  capture seam captures nothing, logs, and lets the run continue. A reporting
  failure must never fail a task.
- The value types live in `internal/domain/workflow/task` (`changes.go`), not in
  package `workflow` and not in `internal/worktree`. Package `workflow` has no
  import of `internal/worktree`, and this subpackage is not projected into the
  interpreted-stage symbol table, so declaring the types here widens nothing and
  needs no symbol-table regeneration. The capture method stays off that surface
  entirely.
- `files` is capped at 200 entries because an event's `data` is not
  length-limited by the store. `totals` always covers the **full** set and
  `truncated: true` says when entries were dropped.
- `binary` means "no textual hunks", not "a binary file". Rename pairing is
  go-git behaviour the lane pins with a test; where the repository records a
  rename as add + delete, the surface reports what was recorded.

## Per-run configuration: a durable event, not a table

**Decision.** The effective configuration an attempt ran under is persisted as a
`config_captured` durable event (`internal/events.KindConfigCaptured`), on the
same attempt key R2 adds — not in a table, and not behind new RPCs.

**Why.** Three reasons, in order of weight:

1. Events are already this system's durable per-attempt provenance stream, and
   they demonstrably carry aggregation (`StageStats`, `TokensByDay` both
   aggregate over persisted events). R3 puts its diffstat there in this same
   change. A table plus two RPCs would have given one feature **two provenance
   mechanisms for one purpose**.
2. It adds no RPC, so the ratified state-store surface is untouched and nothing
   in `docs/prds/state-store-contract.md` (rev 2c) changes.
3. The page already fetches the task's events to build the rail (R1) and to read
   the diffstat (R3), so the document arrives in a fetch it already makes. There
   is nothing to join.

The single-row `config_snapshot` table is deliberately **left alone**. It
answers "what is the running configuration now", is replaced on every publish,
and is pinned to one row by a test. It could not answer "what did attempt 2 run
under" in any case.

**Payload.** The result of `config.Config.ForTask()` — the non-secret
task-runtime subset — plus the schema constant `archie/task-config@1`. Not the
dashboard's configuration view: that is a larger projection carrying the full
schema catalog, and it is the wrong object for this. Stored shape and the tab's
read rule are in `docs/architecture/observability.md`.

**Binding conditions.** It is emitted through the durable path, never the
lossy bus; it is written once per attempt, not once per stage; redaction is
proven **positively** by a canary-secrets test rather than assumed from the
producer's contract; the timeline gains one human line for it rather than
dumping a JSON blob; and an attempt with no such event renders "not captured for
this run", never an empty table implying defaults applied.

**Documented residual — the honest trust claim.** A task-scoped grant may insert
events on its own task ID; that is how `tool_call` and `agent_finish` already
work, so a task can in principle write its own `config_captured` event. This
**widens no capability** — the grant could already insert arbitrary events on
itself — but it does mean this provenance is exactly as trustworthy as the rest
of that task's event stream, and no more. It is not tamper-proofing and must
never be described as such.

## No new RPC and no new table

The entire wire change is two fields on existing messages:

```proto
// proto/state/v1/state.proto
message Event              { int64  attempt = 11; }
message ReadTaskLogRequest { string stage   = 9;  }
```

No RPC was added, no store table was added, and no new configuration field was
introduced. `docs/prds/state-store-contract.md` is unchanged because no ratified
contract changed; the JSON document rides an existing `data` column over an
existing RPC.

Both fields are tolerant in both directions, and the degrade is stated rather
than hidden: an old server makes `attempt` read as `0` and `stage` match
nothing, so the page must say the deployment cannot attribute events to attempts
rather than show one run labelled "attempt 1". An old client shows
`config_captured` as an unknown kind, and the Config tab must then say "this
deployment cannot read per-run configuration" — a different claim from "not
captured for this attempt".

## Retry is a new run

The retry action is reachable from the run view and produces a new attempt that
appears as a new run. It is a **fresh** run: it resets the branch onto
`origin/<base>` (`internal/worktree.(*Manager).refresh` → `resetOnto`) and
**discards the previous attempt's commits**. Labelling it "restart" would imply
resumption that does not happen, so the control and its confirm copy say so.

## Honest limits the surface carries

These are the claims that would otherwise be overstated. Each is a copy or
rendering rule, not a caveat in a comment:

- **There is no exit code, so no stage shows pass/fail.** `stage_finish` carries
  a duration and an optional error string and nothing else. A stage `ok` means
  "returned without error", not "the work was correct". The agent's own verdict
  is shown separately from the rail. A stage that parks after finishing cleanly
  leaves that stage `ok` on a failed task, so attempt status is read together
  with task status.
- **The log stage filter only matches lines the workflow bound a stage to.**
  Agent and tool output carries no stage and never matches. The pane states that
  coverage limit next to the control rather than presenting a filtered view as
  the whole story.
- **Absence and unreadability are different answers.** A deployment that cannot
  read task logs says so; an attempt whose log does not exist says that. They
  are never collapsed, in either direction, for logs or for configuration.
- **An empty changed-files view never means "nothing changed".** No capture
  exists for runs predating this change and the forge cannot supply one, so the
  empty state reads "not captured for this run".
- **No run-duration figure is shown.** Per-stage durations only: a run can spend
  days in `waiting_human`, and one number would understate wall time.
- **No duration is invented** for a stage with no `stage_start`; it reads "not
  recorded".
- **Unattributed history is never presented as an attempt.** Events with
  `attempt = 0` are reported as a count, not guessed into a run.
- **The configuration shown is the attempt's non-secret task-runtime subset** —
  bot identity, models, limits, budgets, dispatch, diff cap, notifications,
  forge host, tool policy. It is not the full dashboard configuration and must
  never imply it contains providers, repositories, identities or credentials.

## Route and compatibility

`/tasks/{id}` is the detail route; it is deep-linkable and reload-safe.
`/tasks?task=N` and `/tasks?status=...` keep working and keep their existing
behaviour, including the nav highlight. The debug view is why the raw view
exists at all: the task list response is capped, so a client-side join has no
row for an older task, and a debug view that silently hid events would be worse
than useless.

## What this deliberately does not do

- **No new RPC, table, store interface or configuration field.** Nothing here
  needed one, and each would have widened a ratified surface.
- **No exit status, no pass/fail gate badge, no "steps" in the CI sense.** The
  reference layout is a CI pipeline; archie's stages are agent runs and the page
  must not borrow CI's claims.
- **No backfill** of attempts, change captures or configurations onto runs that
  predate the change.
- **No client-side join for configuration.** It arrives in the events response
  the page already fetches; there is nothing to join.
- **No change to `docs/prds/state-store-contract.md`.** No ratified contract
  moved.

## Files this change touches

- `internal/store/`: the `events.attempt` column, its migrator arm, and the
  schema-presence test.
- `internal/events/`: `Event.Attempt`, `KindChangesCaptured`,
  `KindConfigCaptured`, `ConfigCapturedSchema`.
- `internal/domain/workflow/task/changes.go`: the change value types.
- `internal/worktree/` + `internal/app/agentworker/`: the capture primitive and
  its production forwarding.
- `internal/domain/workflow/steps.go`: the capture at commit and push.
- `internal/daemon/`: the per-attempt configuration emit at dispatch.
- `internal/logging/`: `Query.Stage`.
- `internal/webui/`: the three read endpoints and the `stage` log parameter.
- `proto/state/v1/state.proto` + generated stubs: the two field additions.
- `ui/src/`: the detail page, the tab primitive, the rail, and the panel copy.
- `docs/`: this document and `docs/architecture/observability.md`.

## Acceptance

1. Two attempts of one task render as two runs, and attempt 1's rail excludes
   attempt 2's stages.
2. A task with no stages renders "no stages recorded", never an unexplained
   empty rail.
3. A pre-change `archie.db` gains the `attempt` column with `user_version`
   still 3, and its old events are reported as unattributed.
4. An attempt with a capture shows per-file added/deleted counts, status, repo
   and PR links; an attempt without one says so and never says "no changes".
5. The Config tab shows the attempt's document for the selected attempt only,
   with no secret value present in the serialized document.
6. Retry from the run view produces a new attempt shown as a new run, with copy
   stating the previous attempt's commits are discarded.
7. The log stage filter narrows to stage-bound lines and the pane states the
   coverage limit; `disabled` and `found=false` remain distinguishable.
8. `/tasks/{id}` survives reload, and `/tasks?task=N` / `/tasks?status=...`
   are unchanged.
9. The debug view returns the task record and its unfiltered events, each
   carrying `attempt`.
10. Run the gate and the dashboard build: `task check` passes clean, `task ui`
    has been run so `ui/dist` matches the sources, and every touched package
    builds and passes under `-race`.
