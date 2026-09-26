# Workflows calling workflows

**Status:** Draft
**Date:** 2026-09-26
**Bead:** `archie-core-t2db.41`
**Extends:** `docs/prds/event-automation.md` (workflow/binding/profile model), `docs/prds/state-store-contract.md` (two new RPCs), `docs/prds/execution-tree-state-machine.md` (call kind)

## The call, end to end

```
caller run (in agent container)
  workflow.call step ──StartCall──▶ State Store EnqueueCallTask(caller, workflow, inputs)
                                      │  callee task row: org/identity/owner/repo
                                      │  derived from the caller row server-side
                                      ▼
                                   callee task (queued, org of the caller)
                                      │  daemon claims, pins the callee's OWN
                                      │  definition → its OWN agent profile
                                      ▼
                                   callee run (its own container, own tree)
  wait: true  ──poll──▶ State Store WorkflowCallStatus(caller, callee)
  wait: false ──continue
```

A `workflow.call` step starts the callee as a first-class task and, with
`wait: true`, waits for its terminal state. The callee is never run "inside"
the caller: it gets its own task row, its own pinned definition, its own
profile, its own container, and its own outcome on its own run
(`execution-tree-state-machine.md`).

## Save-time checks (the collection is the authority)

All semantic checks run in `workflow.ValidateDefinitionCollection`, the gate
every save and load of the `workflow-definitions` resource passes through.
A single-definition parse cannot see callees, so it validates shape only.

- **Callee exists.** A call to a workflow outside the collection is refused.
- **Inputs checked.** A call value is a literal or a reference
  (`inputs.<name>`) to the calling workflow's declared input. References must
  name a declared input; the resolved type must satisfy the callee's declared
  type; unknown callee inputs are refused; a required callee input must be
  assigned.
- **Self-calls refused.** The call graph over the collection is walked from
  every workflow; a workflow that reaches itself, directly or through other
  workflows, is refused with the cycle path. (Cycles can otherwise deadlock:
  `wait: true` callers hold their containers.)
- **Repository.** `workflow.call` needs no repository, so it is legal in a
  `repository: none` workflow: the callee inherits the caller's task record
  (owner/repo/identity/org), and the callee's own `repository` mode decides
  whether its run clones.
- **Org enablement** (bead `archie-core-t2db.42`): the single
  instance-wide collection the current model serves is what "enabled for
  the caller's org" resolves against — an undefined callee parks. The
  callee's org is derived from the caller's row, so a callee is never
  visible outside its caller's org. The per-org enable/disable catalogue
  lands with `archie-core-t2db.42` and becomes the one source of this
  predicate.

## Runtime

**Depth limit.** `workflow.MaxCallDepth` (5). The calling stage refuses a call
that would exceed it; the store re-checks on insert (callee depth = caller
depth + 1), so no path can enqueue past the limit.

**Starting the callee.** The caller runs inside a container whose task-scoped
State Store grant authorizes only `Update`/`Transition`/`InsertEvent` on its
own task — it can neither enqueue a task nor read another one. One new
contract RPC starts the callee:

- `EnqueueCallTask(caller_task_id, workflow, inputs_json) → task`
  — the store reads the caller row, refuses a caller that is not `running`,
  refuses past the depth limit, and inserts the callee row deriving org,
  workspace, identity, owner, repo and a fresh synthetic issue number from
  the caller (the same org COALESCE `EnqueueIssue`/`InsertChatTask` use). The
  callee row carries `call_parent_task_id` and `call_depth`.

**Waiting on the callee.** One new read RPC returns a call's callee:

- `WorkflowCallStatus(caller_task_id, call_task_id) → (status, detail)` — the
  handler verifies `call_parent_task_id` matches, so a caller reads only its
  own callees, and returns the callee's status plus its latest transition
  detail (the callee's summary — the wait:true "outputs" until a structured
  outputs model lands).

Both RPCs are admitted to task-scoped grants on `caller_task_id == granted
task ID` (`authorizesTaskScopedCall` gains two request-shape cases). This is
the one sanctioned widening of a task grant: the callee is the caller's child,
and starting it is the mechanism by which one event becomes several runs. The
widenings stay request-shape checks; the parent-child authorization is a
handler-side row check, so the interceptor never reads the database.

**Schema.** `tasks` gains `call_parent_task_id` (bigint, default 0) and
`call_depth` (int, default 0); both cross the wire on the `Task` message.
Columns default so existing writers and rows read as depth-0 root runs.

**The engine capability.** The workflow domain owns a consumer facade,
`task.Caller` (2 methods), carried by `*staterpc.Client` and wired into
`TaskContext.Calls` by the agent worker's composition root — the same shape
as `workflow.Store`. A runner with no `Calls` capability fails a
`workflow.call` step with a named error rather than pretending.

**Semantics.**

- `wait: false` — start, record a durable `workflow_call_started` event, and
  continue. The callee's outcome is reported on its own run.
- `wait: true` — start, then poll `WorkflowCallStatus` (2 s, bounded by the
  task's wall clock) until the callee is terminal. `completed`, `pr_open` and
  `merged` succeed the step; `parked`, `failed`, `rejected`, `dead` and
  `closed_wont_do` fail it with the callee's detail in the error. A durable
  `workflow_call_finished` event carries the terminal status and detail.
- The caller's own stage/attempt accounting is unchanged: the call is one
  stage that took as long as its callee.
- A callee inherits the caller's task record but is otherwise independent:
  retry, park and cancel of the callee never rewrite the caller. Cancelling a
  caller cancels its callees through the operator action on each
  (`execution-tree-state-machine.md` "Cancellation"); until the tree state
  machine lands, a caller cancelled while waiting fails its stage and leaves
  the callee running — the same semantics as `wait: false`.

## What is deliberately not here

- **Declared outputs.** `wait: true` receives the callee's terminal detail,
  not structured outputs; the outputs model is its own bead.
- **The StepExecution tree.** The `call` kind is designed for in
  `execution-tree-state-machine.md`; this design lands the behaviour without
  the per-step records.
- **Per-org enablement** — bead `archie-core-t2db.42`'s catalogue is the
  source once it exists.