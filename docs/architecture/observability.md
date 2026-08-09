# Task Observability

**Status:** Approved, not yet implemented
**Date:** 2026-08-09

## Problem

archied's own daemon logging (`internal/logging/`) is solid: a rotating file
sink, a live `Feed` the dashboard subscribes to, and a reader the `/api/logs`
endpoint serves from. None of that reaches inside a task.

A task's actual work happens in an `archie-agent` container, dispatched over
NATS (`[agent] mode = "nats"` is the only supported mode; the in-process
runner exists for tests and is being migrated away — see
[Decision: no new functionality on non-NATS transports](#decision-no-new-functionality-on-non-nats-transports)).
Four things currently destroy the evidence of what that container did:

1. **`cmd/archie-agent/main.go`** logs to `os.Stderr` only
   (`log := slog.New(slog.NewJSONHandler(os.Stderr, nil))`). Nothing captures
   it outside the container.
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

This matters beyond debugging convenience: the deployment model
(`CLAUDE.md` "Deployment model") is a systemd daemon plus ephemeral Docker
containers that may run unattended for weeks between operator logins, and is
intended to be usable by people other than the original operator. A system
that is only debuggable by an operator who happens to be watching `docker ps`
in real time does not meet that bar.

## Decision: NATS is the only transport this feature targets

`[agent] mode = "nats"` is the supported deployment; the agent image must
already be able to reach NATS in every deployment, because it is the *only*
channel carrying stage results back to the daemon — if it can't reach NATS,
the task cannot complete regardless of logging. The in-process runner
(`agentexec.InProcessRunner`) exists for tests and is being migrated away.
New functionality is NOT added to it or to any non-NATS path. A container
that dies before establishing a NATS connection is a distinct, narrower
failure (connectivity/config) from the one this document addresses (a
connected container's own output being unrecoverable), and is out of scope
here.

## Design

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

`cmd/archie-agent/main.go` replaces the stderr-only `slog.Handler` with one
that tees to stderr (kept for container-level operator debugging when a
shell is attached) and publishes each record to
`agentexec.SubjectForSystem(taskID)`, which already exists for this purpose
and already has zero consumers to conflict with. The agent already holds its
NATS client and task ID (`bootTaskID`, read from
`<worktree>/.git/task.json` per the container/pool.go trap documented in
`CLAUDE.md`). Publishing is fire-and-forget with a bounded local buffer: log
shipping must never block or fail the run it is reporting on.

### Daemon side

Subscribe `agentexec.SubjectAgentWildcard` ("archie.agent.>") for the
`.system` suffix once at startup (app-layer wiring — this composes the
domain's log sink with the infrastructure NATS subscription, so it lives in
`internal/app/`, not in `internal/domain/workflow` or `internal/nats`
directly). Demux by task ID, append to that task's sink, and also publish
into the existing `logging.Feed` so live task output appears on the
dashboard exactly like daemon output does today.

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
directory. Prune a task's log directory when `nell.ArchiveTask`
(`internal/nell/adapter.go`) archives it, so task logs share the task's own
lifecycle rather than accumulating independently.

### Surfaces

- `/api/tasks/{id}/logs`, built on `logging.Reader` the same way
  `/api/logs` is today.
- A chat tool so Archie can answer "why did task 4 park?" directly from
  Telegram or the webui chat — this is the surface that actually matters
  for the deployment model in `CLAUDE.md`, where an operator may not be
  looking at a dashboard at all.

## Dependency direction

`internal/logging` remains cross-cutting per `organisation.md` — it imports
no domain, infrastructure, or app package, so extending it to be task-scoped
does not change its classification. The workflow domain writes through an
interface it declares (a `TaskLogSink` or equivalent, not a direct
`internal/logging` import into `internal/domain/workflow`, unless
`internal/logging` is judged to already be an acceptable cross-cutting
dependency for a domain package — confirm against `organisation.md`'s
concrete rule before writing the interface). `internal/app` wires the NATS
subscription to the sink; `internal/agentexec` and `internal/nats` supply
the transport underneath.

## Sequencing

1. Gate-output capture (~30 lines, no NATS changes) — fixes the immediate
   symptom that prompted this document.
2. Task log sink in `internal/logging`.
3. Agent-side publisher on `SubjectForSystem`.
4. Daemon-side subscriber + `logging.Feed` integration + retention on
   archive.
5. `/api/tasks/{id}/logs` + chat tool.

Tracked as GitHub epic/subtasks per the issue list linked from this
document's PR.
