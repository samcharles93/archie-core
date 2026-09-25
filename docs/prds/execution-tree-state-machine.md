# Execution tree and enforced state machine

**Status:** Draft
**Authority:** `docs/architecture/agent-system.md` (canonical workflow model);
settles the "enforced WorkflowExecution state machine" item in
`docs/architecture/migration-decisions.md` section 4.
**Compounds with:** `docs/prds/task-run-detail.md` (attempt attribution),
`docs/prds/state-store-contract.md` (wire contract and task-scoped grants).

## Decision

A WorkflowExecution becomes the root of a tree of StepExecutions. Every stage
run, and every agent call a stage makes, is its own durable record with a
parent, an attempt, a status and an outcome. Both levels move only through a
declared transition table, enforced by the State Store in the same transaction
that writes the audit row and the domain event.

## Problem

- A run is one `tasks` row. The only per-step record is the mutable `stage`
  column, overwritten as `workflow.Run` advances, plus observability events.
  A failed attempt cannot say which step failed or what its children did
  without replaying the event stream.
- `Store.Transition` guards on the expected `from` status, but any `from` to
  any `to` is accepted. A `queued` to `merged` write succeeds.
- Stop cancels one context in `daemon.runningTasks`. Nothing persisted says
  which work was in flight, so an agent call that outlives its stage (a
  reviewer, a container) is invisible to the store.
- `workflow.Run` writes `tc.Store.Update` and discards the error, then emits
  `KindStageStart` separately. State and events can diverge.

## Model

```text
WorkflowExecution (tasks row, id E)
└── attempt N
    ├── StepExecution  stage "plan"           depth 0
    ├── StepExecution  stage "implement"      depth 0
    │   └── StepExecution agent "implement"   depth 1
    └── StepExecution  stage "review"         depth 0
        ├── StepExecution agent "review:1"    depth 1
        └── StepExecution agent "review:2"    depth 1
```

- A StepExecution belongs to exactly one WorkflowExecution and one attempt.
  `parent_id` is null for a stage and names the enclosing StepExecution
  otherwise. Depth is derived from the parent, never supplied by the caller.
- `kind` is `stage` or `agent`. A new kind needs a named consumer before it is
  added.
- A retry starts a new attempt. Earlier attempts' StepExecutions are never
  rewritten.

## Transition tables

WorkflowExecution statuses are the existing `taskstate` constants, persisted
strings unchanged. `taskstate` gains the one table every writer checks:

| From            | To                                                         |
| --------------- | ---------------------------------------------------------- |
| `queued`        | `running`, `closed_wont_do`                                |
| `running`       | `queued` (crash recovery), `waiting_human`, `pr_open`, `completed`, `parked`, `closed_wont_do` |
| `waiting_human` | `queued` (approve), `closed_wont_do`                        |
| `parked`        | `queued` (retry), `dead` (abandon), `closed_wont_do`        |
| `pr_open`       | `merged`, `rejected`, `queued` (remediation), `closed_wont_do` |
| terminal        | none                                                       |

StepExecution statuses:

| From      | To                                                     |
| --------- | ------------------------------------------------------ |
| `pending` | `running`, `cancelled`                                 |
| `running` | `succeeded`, `failed`, `cancelled`, `interrupted`      |
| terminal  | none                                                   |

`interrupted` means the process running the step died or shut down. It is
distinct from `cancelled`, which an operator or a parent asked for, and from
`failed`, which the step reported.

A StepExecution cannot reach `running` while its WorkflowExecution is not
`running`, and cannot start under a terminal parent.

## Enforcement

- The table lives in `taskstate` (no dependencies), so the store, daemon and
  gateway share one copy.
- The State Store rejects an illegal transition with a new sentinel,
  `ErrIllegalTransition`, carried across the wire through `mapError` like
  `ErrStaleTransition`. A stale `from` stays `ErrStaleTransition`.
- Each transition is one transaction: guarded row update, audit row, domain
  event row. The events table is written by the store, not emitted beside it.
  `KindStageStart` and `KindStageFinish` are produced by the step transition.
- `workflow.Run` stops discarding persistence errors. A failed step write
  parks the execution; the step does not run unrecorded.

## Cancellation

Stopping a run is one store call, `CancelExecution(E, reason)`. In one
transaction it moves every non-terminal StepExecution of the current attempt
to `cancelled` and the WorkflowExecution to `closed_wont_do` or `parked`,
whichever the operator action names. The daemon then cancels the in-memory
context. A worker whose step write returns `ErrStaleTransition` because its
step is already `cancelled` stops without writing further.

The store is the record of cancellation. The context cancel is the delivery
mechanism, so a lost cancel still leaves the store correct and the worker's
next write fails.

## Crash recovery

`RecoverStale` moves every `running` StepExecution of an interrupted
execution to `interrupted` in the same transaction that requeues the
execution. The next attempt starts with fresh steps.

## Wire contract

New State Store RPCs, each with a matching Go method on a narrow consumer
facade:

- `StartStep(execution, attempt, parent, kind, name) -> step id`
- `FinishStep(step, from, to, detail, tokens_used)`
- `CancelExecution(execution, reason, to)`
- `ListSteps(execution, attempt)`

`TaskGrants.UnaryInterceptor` authorises a task-scoped token for `StartStep`
and `FinishStep` on its own execution only. The container records its own
agent calls. `CancelExecution` and `ListSteps` require the administrative
token.

## Storage

One new table, `step_executions`: `id`, `execution_id`, `attempt`,
`parent_id`, `depth`, `kind`, `name`, `status`, `detail`, `tokens_used`,
`started_at`, `finished_at`. Indexed on `(execution_id, attempt, id)`.
Existing rows gain no backfilled steps. Their history stays in `transitions`
and `events`.

`tasks.stage` is written from the step transition while any reader still uses
it, then dropped once the dashboard reads `ListSteps`.

## Out of scope

- Running sibling steps in parallel. The tree records concurrency; changing
  the stage runner is separate work.
- Cost in currency. `tokens_used` per step is the only usage field.
- Workflow definition versioning and the `tasks` to `workflow_executions`
  rename.
- A harness contract for swappable coding agents.

## Plan

1. `taskstate` transition tables with table-driven tests covering every
   legal and a sample of illegal pairs.
2. `ErrIllegalTransition` enforced in the Postgres store and mapped over the
   wire.
3. `step_executions` migration, `StartStep`/`FinishStep`, grant rules;
   `workflow.Run` records stages; events written in the transition
   transaction.
4. Agent-call children recorded from the agent runtime.
5. `CancelExecution`; daemon stop and chat/dashboard actions route through it.
6. `RecoverStale` interrupts running steps.
7. Dashboard run detail reads `ListSteps`; `tasks.stage` dropped.

## Verification

- An illegal WorkflowExecution or StepExecution transition returns
  `ErrIllegalTransition` over gRPC, and `errors.Is` matches on the client.
- Killing the daemon mid-stage, then restarting, leaves that stage
  `interrupted` and the execution `queued` with a new attempt.
- Stopping a run during review leaves every review child `cancelled`, and a
  late `FinishStep` from the container returns `ErrStaleTransition`.
- A task-scoped token cannot `StartStep` on another execution.
- For every step transition there is exactly one event row, written in the
  same transaction.
