# Task Observability

**Status:** Implemented. The per-run detail surface was added on 2026-09-18 and
is described below.
**Date:** 2026-08-09 (revised 2026-09-18)

## Historical problem

archied's own daemon logging (`internal/logging/`) was already solid: a
rotating file sink, a live `Feed` the dashboard subscribes to, and a reader
the `/api/logs` endpoint serves from. Before this implementation, none of
that reached inside a task.

A task's actual work happens in a task-scoped `archie-agent` container,
dispatched as one complete workflow over NATS.
Before this design was implemented, five things destroyed the evidence of what
that container did:

1. **Worker startup** constructed an stderr-only logger and never replaced its
   handler after discovering a dedicated boot task, so runtime records could
   not leave the container.
2. **`internal/container/pool.go`** creates every task container with
   `AutoRemove: true`. The container — and its stderr — is gone at exit,
   success or failure alike.
3. **`internal/domain/workflow/implement.go`, `StageBaselineGate`** captures
   `go build`/`go vet`/`go test` output via `cmd.CombinedOutput()`, then
   clips it to 200 characters into a `Warn` log line and discards the rest.
   The eventual park reason is built from `res.Status` alone — the actual
   compiler error is never persisted anywhere a human or Archie's own chat
   tools can read it back.
4. **`agentexec.Result`** (`internal/agentexec/protocol.go`) carries only
   `Summary`/`Detail` — no step-by-step transcript (tool calls, durations,
   errors). There is no transcript to recover even in principle.
5. **`agentexec.SubjectForSystem`** is documented as the return channel for
   exactly this — *"log dumps, health, and PII warnings... the daemon reads
   these for observability and never forwards them"* — but has no publisher
   and no consumer anywhere in the codebase except a subject-format
   assertion in `nats_test.go`. The designed return path was never wired.

The practical consequence: a task parks with
`stage baseline: baseline red -- go build ./... fails and builder could not
auto-fix (status: parked)` and there is no way — not from the dashboard, not
from Telegram chat, not by SSH'ing into the deployment host — to see *why*
`go build` failed. The container that knew is already gone.

This matters beyond debugging convenience: supported deployments can combine
a resident daemon with ephemeral Docker containers that run unattended for
weeks between operator logins, and the system is
intended to be usable by people other than the original operator. A system
that is only debuggable by an operator who happens to be watching `docker ps`
in real time does not meet that bar.

## Decision: NATS is the sole autonomous handoff

The agent image must reach NATS in every deployment, because the broker is the
only autonomous task handoff and the only channel carrying task results back
to the daemon. `archied` has no workflow runner or host fallback. A container
that dies before establishing a NATS connection is a distinct, narrower
failure (connectivity/config) from the one this document addresses (a connected
container's own output being unrecoverable), and is out of scope here.

## Implemented design

The key property: **logs leave the container while it is alive**, so its
death at `AutoRemove` no longer matters. Persistence happens in the daemon
process, which already owns durable state.

### Sink: extend `internal/logging`, not a new package

`internal/logging` already owns its on-disk format end to end per
`organisation.md` ("a package owns its own format end to end"). Add a
task-scoped writer producing the same `logging.Entry` JSONL shape at
`<state_dir>/logs/tasks/<task_id>/attempt-<n>.jsonl`. `logging.Tail`,
`logging.Query`, and `logging.Components` operate on this unchanged;
`internal/webui/api_logs.go` stays transport-only, as it does today.

### Agent side: make the designed subject real

`internal/app/agentworker` replaces the stderr-only `slog.Handler` with one
that tees to stderr (kept for container-level operator debugging when a
shell is attached) and publishes each record to
`agentexec.SubjectForSystem(taskID)`, which already exists for this purpose.
The application layer obtains only a narrow publisher from
`internal/infrastructure/agenttransport/nats`; raw core-NATS types remain in
infrastructure. `internal/infrastructure/agentboot.TaskID` reads the task ID
from `<worktree>/.git/task.json` per the container/pool.go trap documented in
`CLAUDE.md`. Publishing is fire-and-forget: log shipping must never block or
fail the run it is reporting on.

### Daemon side

`cmd/archied/main.go` registers `subscribeSystemLogs` once at startup on
`agentexec.SubjectSystemWildcard`. It demultiplexes by task ID, appends to that task's
sink, and publishes into the existing `logging.Feed` so live task output
appears on the dashboard exactly like daemon output. The approved target is
for this composition to move to `internal/app/archied`; that package does not
yet exist, so this document does not claim the migration is complete.

The daemon is also a writer in its own right for the decisions only it makes.
Every park records its reason into that attempt's sink (`daemon.recordPark`),
on the daemon side after the guarded transition lands and in
`workflow.park` on the agent side, because "why did this park?" is the question
the log exists to answer and the container path cannot answer a run that never
reached a container. The entry is written only once the park actually
happened, so a transition another actor won does not leave a log claiming an
outcome the task board contradicts.

### Gate output: stop discarding it

`StageBaselineGate` and the equivalent TDD-stage gate runs write full
`cmd.CombinedOutput()` to the task's sink under a `gate` component, and put a
bounded tail (~2KB, not 200 bytes) into the park reason. This is the cheapest
fix and independently valuable — it alone would have made the `go build`
failure diagnosable without any NATS plumbing.

### Transcript

Step-level events (tool name, argument digest, duration, error) are emitted
as log entries on the same NATS system subject rather than added as new
fields on `agentexec.Result`. This keeps the wire protocol
(`internal/agentexec/protocol.go`) stable and the transcript lands in the
sink automatically, with no separate delivery path to keep consistent.

### Retention

Reuse the existing rotation (`MaxSizeMB`/`Keep`) semantics per task
directory. Prune a task's log directory when `store.ArchiveTask` archives
it, so task logs share the task's own
lifecycle rather than accumulating independently.

### Surfaces

- `/api/tasks/{id}/logs`, built on `logging.Reader` the same way
  `/api/logs` is today.
- A chat tool so Archie can answer "why did task 4 park?" directly from
  Telegram or the webui chat — this is the surface that actually matters
  for the deployment model in `CLAUDE.md`, where an operator may not be
  looking at a dashboard at all.
- The per-attempt read surface described in the next section, on top of the
  same task-scoped stream.

## Per-run task detail (attempts, stages, changes, config)

**Status:** landed, together with the change set that carries this revision
(workflow run `76f8e877`). The provenance half is in the tree: `events.Event.Attempt`,
the `changes_captured` and `config_captured` event kinds, and the two protobuf field
additions. So is the read half: the three per-attempt endpoints
(`/api/tasks/{id}/attempts`, `/changes`, `/debug`) and the dashboard run-detail page
that consumes them. The decision record, the rejected alternatives and the honest
limits are `docs/prds/task-run-detail.md`.

The task log surface above answers *why did this park?*. This surface answers
*what did this run actually do?*, per attempt.

### Attempt attribution

One task can run many times, and the task's event stream is the durable record
of all of them. `events.Event.Attempt` attributes each event to one run; `0`
means **not attributable** — every event written before this change, plus the
deliberately task-agnostic producers. An unattributed event is reported as a
count and is never presented as an attempt.

Attribution is a column, not a fold over `task_queued`/`task_retried`: a fold
would have to guess which side of a boundary an event belongs to across a daemon
restart, a stale-run recovery, or two interleaved attempts, and a guess rendered
as structure is exactly the false fidelity this surface exists to avoid.

The column is part of the events table in
`internal/infrastructure/postgres/migrations/0001_state_store.sql`, and nothing
is backfilled.

### Read surface

The routes are registered beside the existing task routes in
`internal/webui`'s `registerTaskRoutes` and all resolve the task through the
store's `TaskByID`; a task that does not exist is a `404`, never a `200` with an
empty body.

- `GET /api/tasks/{id}/attempts` — every attempt with its stages inline, so the
  rail and the attempt selector can never disagree. Stages are ordered by
  occurrence within their attempt. An attempt with no stages is legitimate and
  renders as "no stages recorded", never as an unexplained empty rail.
- `GET /api/tasks/{id}/changes?attempt=N` — the change captures for one
  attempt, ordered by capture time; more than one per attempt is normal (a
  mid-workflow commit such as TDD's `commit-repro`, then `commit-push`, then a
  final `open-pr` capture that carries the pull-request number).
  `found: false` means no capture was recorded for that attempt, which is
  **not** "no files changed"; a capture that was recorded but could not be read
  is reported as exactly that, and never as a diffstat of zeroes.
- `GET /api/tasks/{id}` — unchanged: the raw event array, each event now
  carrying `attempt`. The per-attempt configuration is read from this response,
  selected by kind **and** attempt.
- `GET /api/tasks/{id}/logs` — gains `stage` beside its existing filters. The
  match is exact and case-insensitive, and an entry that records no stage never
  matches (see `logging.Query.Stage`).
- `GET /api/tasks/{id}/debug?attempt=N` — the stored task record and the task's
  events, deliberately unfiltered, so an operator can attribute everything the
  task did even when a panel has nothing to show.

The `attempt` parameter follows one rule on every route: absent or `0` selects
the task's current attempt, and the resolved attempt is echoed in the response.

### Stage status

`stage_start` and `stage_finish` are the only inputs, so the derivation is
explicit and each case is pinned by a test:

| Events seen for the stage | Reported |
| --- | --- |
| `stage_finish` with `data.error` | `failed`, with the error text |
| `stage_finish` with `data.interrupted` | `interrupted` |
| `stage_finish`, neither | `ok` — "returned without error" |
| `stage_start` unmatched, and this is the task's current in-flight attempt | `running` |
| `stage_start` unmatched on any dead attempt | `interrupted` — the run ended without finishing it |
| `stage_finish` with no matching start | the stage, with no start time invented |
| no stage information at all for that attempt | `unknown` |

**There is no exit code.** `stage_finish` carries a duration and an optional
error string and nothing else, so no stage can be shown as pass/fail and the
page must not imply verification. A stage that finished cleanly and then parked
stays `ok` on a failed task, which is why the attempt status is read together
with the task status rather than on its own.

### Changed files

The change an attempt produced is read from the worktree at the moment it is
committed or pushed, and persisted as a `changes_captured` durable event — plus
one final capture when `OpenPR` records the pull-request number, which is the
only point at which a capture can carry one. It has to be captured there: the
worktree is deleted on merge, close and no-PR terminal states, a retry resets
the branch onto its base, and no forge contract exposes a changed-file read.
`internal/domain/workflow/task/changes.go` owns the value types and the status
strings they persist.

- One capture carries per-file `added`/`modified`/`deleted`/`renamed`/
  `typechange` status with added and deleted line counts, and totals.
- Each capture names its schema (`archie/task-changes@1`, in `internal/events`)
  and a reader refuses a payload whose schema or key set it does not know,
  rather than rendering the zero value of every field it could not find. That
  refusal surfaces as "a capture was recorded but could not be read", which is
  a different statement from "no capture was recorded".
- `base_sha` is the merge base the file list was diffed against, not the base
  branch's tip, so the pair describes one change set even when the base branch
  moved during the run.
- `files` is capped; `totals` always covers the **full** set, and a truncated
  capture says so rather than silently dropping entries.
- `binary` means "no textual hunks" — never a claim about the file's type on
  disk — and a path the repository recorded as a rename pair is reported as the
  repository recorded it.
- Capture **degrades and never parks**: an implementation without the capture
  seam captures nothing, logs, and lets the run continue.

### Per-attempt configuration

The effective configuration an attempt ran under is persisted as a once-per-
attempt `config_captured` durable event carrying the schema constant
`archie/task-config@1` and the non-secret task-runtime subset from
`config.Config.ForTask()`. It is not stored in a table and has no endpoint of
its own: the dashboard reads it out of the events response it already fetches.
The single-row `config_snapshot` table is left alone — it answers what the
running configuration is now and is replaced on every publish.

Redaction is proven positively by a test that builds a full configuration
containing canary values and asserts none of them survive serialising, rather
than being inferred from the producer's contract. The timeline carries one
human line for this kind; the document itself is rendered by the panel.

**Trust boundary, stated honestly.** A task-scoped grant may insert events on
its own task ID — that is how `tool_call` and `agent_finish` already work — so a
task could in principle write its own `config_captured` event. This widens no
capability, because the grant could already insert arbitrary events on itself,
but it does mean this provenance is exactly as trustworthy as the rest of that
task's event stream, and no more. It is not tamper-proofing and must never be
described as such.

### The wire

The whole wire change is two fields on existing messages: `Event.attempt`
(field 11) and `ReadTaskLogRequest.stage` (field 9) in
`proto/state/v1/state.proto`. No RPC, no table and no configuration field was
added, so the ratified state-store contract in
`docs/prds/state-store-contract.md` rev 2c is unchanged.

Version skew degrades instead of failing, and each degrade is a statement the
page has to make rather than hide: an old server makes `attempt` read as `0` and
`stage` match nothing, so the page says it cannot attribute events to attempts
instead of labelling the whole stream "attempt 1"; an old client sees
`config_captured` as an unfamiliar kind, and the panel must then say it cannot
read per-run configuration — a different claim from "not captured for this
attempt".

### Rules the surface keeps

- "No record" and "this deployment cannot read this" stay different answers,
  for logs and for configuration alike.
- No run-duration figure is shown. A run can spend days in `waiting_human`, so
  one number would understate wall time; per-stage durations only.
- No duration is invented for a stage with no start; it reads "not recorded".
- The stage filter is a narrowing, not a coverage claim: agent and tool output
  carries no stage and is never matched, and the pane says so beside the
  control.
- An attempt predating the change shows "not captured for this run" for its
  configuration and its change capture, never an empty view that implies
  defaults applied or nothing changed.

### Failure and rollback

Every part is additive. A capture or a configuration emit that cannot happen
leaves the run alone — reporting must never park a task. Reverting the change
set removes the endpoints and the page; the extra column and the extra event
kinds are inert to the previous revision (a column no statement names is
ignored, and an unfamiliar event kind already renders as unfamiliar), and there is
no data transformation to undo.

## Dependency direction

`internal/logging` remains cross-cutting per `organisation.md` — it imports
no domain, infrastructure, or app package, so extending it to be task-scoped
does not change its classification. The workflow domain writes through the
cross-cutting logging contract. On the agent side,
`internal/app/agentworker` composes the narrow publisher supplied by
`internal/infrastructure/agenttransport/nats`. On the daemon side,
`cmd/archied/main.go` currently composes the NATS subscription with the task
sink until the approved `internal/app/archied` migration is implemented.

## Implementation sequence (completed)

1. Gate-output capture (~30 lines, no NATS changes) — fixes the immediate
   symptom that prompted this document.
2. Task log sink in `internal/logging`.
3. Agent-side publisher on `SubjectForSystem`.
4. Daemon-side subscriber + `logging.Feed` integration + retention on
   archive.
5. `/api/tasks/{id}/logs` + chat tool.

Tracked as GitHub epic/subtasks per the issue list linked from this
document's PR.
