# Declared outputs of a workflow run

**Status:** Draft
**Date:** 2026-09-30
**Bead:** `archie-core-t2db.43`
**Authority:** `docs/prds/event-automation.md` for what a declared output is,
`docs/prds/workflow-calls.md` for the call and its grants,
`docs/prds/state-store-contract.md` for the State Store contract the wire
change must satisfy.
**Supersedes:** `docs/prds/workflow-calls.md`, "Waiting on the callee": the
callee's transition detail is the human summary, not the stand-in for
structured outputs.

A workflow declares named, typed outputs. A run writes each one once. A
`wait: true` caller receives them, and may publish one as its own.

## Where outputs are declared

`outputs:` on the workflow definition, beside `inputs:`, decoded into
`task.WorkflowInterface.Outputs map[string]OutputSpec` where
`OutputSpec{Type, Required}`:

```yaml
id: firewall-contain
outputs:
  contained: { type: bool, required: true }
  summary: { type: string }
```

Names and types are the input grammar: the same identifier shape, the same
type set, and the same check in `WorkflowInterface.Validate`, which both
`ParseDefinition` and `ParseWorkflowInterface` call.

**Forced by:** `ParseDefinition` decodes with `KnownFields(true)`, so a key no
decoded struct declares is refused — the declaration must live on
`WorkflowInterface` for an author to write it at all, and that is where
`docs/development/workflows.md` puts every field a process without the engine
must read. The type set is the shared value vocabulary: `TypeAccepts` and
`ValueType` already check a mapped parameter and an input with it. `required`
defaults to false, and the generated YAML for a builtin that declares no
outputs is unchanged, because a definition's digest pins a run.

**Rejected:** outputs per step, or per call step, because a workflow returns a
result as itself rather than as one of its stages. Also `task.Definition`
gaining outputs: no reader needs them — a binding offers inputs, and a call's
outputs assignment is checked against the callee's YAML in the same
collection — and a declared field with no reader parses and does nothing.

## How a run writes one

Each declared output is offered to the run's agent stages as a capture tool
named after the output, whose arguments are `{"value": ...}`; the accepted
call's `value` member is the output's value. The engine holds the accepted
values on the task context and writes them into its own task row in the
`Store.Update` call the run already makes. A deterministic stage may set the
same values in Go, and the run's finish check validates them identically.

**Forced by:** a run's only structured results are capture calls
(`agentexec.CaptureTool`, `Result.Captures`), and the workflow interface
already declares when a stage needs them (`task.WorkflowNeeds.Captures`).
`WorkflowInterface.Needs` reports `captures` for a non-empty `Outputs`, so
declaring an output alone requires the capability and a Kit profile that
serves no capture tools is refused before dispatch.
Deriving the tools from the declaration means an output cannot be written
under a name nobody declared, and it keeps one route to structured agent
output rather than adding a second beside the capture path. The row write is
the run's existing write: the task row is where a run's own state lives, and
the container already holds `Update` on its own row.

The tool is offered with `MaxCalls: 1`, and a stage is not offered an output
the run has written, so a run writes each output once: a repeat within a stage
is answered with the tool's rejection and the agent carries on, and a later
stage cannot overwrite a value. A stage's own capture tool may not share a
name with a declared output, because the value would otherwise be ambiguous.

**Rejected:** a `workflow.output` step type, because assigning the run's
result from a step needs a grammar for "the previous stage's result" that does
not exist, and a step that reaches the agent would be the second path
`docs/development/workflows.md` forbids. Also a write RPC of its own: the
container's `Update` already reaches its own row, and a second write verb for
one column is a second thing to keep in step.

## How a caller receives one

On a successful `wait: true` call the caller's stage holds the callee's
outputs as a `map[string]any`. A call step's `outputs:` setting publishes one
as one of the caller's own outputs:

```yaml
steps:
  - type: workflow.call
    workflow: firewall-contain
    wait: true
    inputs: { src_ip: inputs.src_ip }
    outputs: { contained: outputs.contained }
```

The key names the callee's declared name; the value names one of the caller's
own with the prefix the call's `inputs:` already uses. A published value joins
the caller's output set for its run, so a caller returns its callee's result to
its own caller, and a run may publish a callee's output and write its own
others.

Lifetime is the attempt. The run starts with an empty output set, every later
stage of that attempt sees the values, and the finish write replaces the row's
set wholesale — so nothing an attempt wrote survives into the next, and a run
that writes nothing stores nothing. Values reach a caller only from a
successful terminal state: the caller assigns them after `callSucceeded`, so a
parked or failed callee delivers nothing whatever its row holds. A call step
with `wait: false` and an `outputs:` block is refused when the definition is
saved, because nothing has finished when the caller moves on.

**Forced by:** caller and callee are separate runs in separate containers, so
the caller's own row is the only durable place to hold a received value. The
prefix rule is not a new grammar: in `inputs.<name>` the prefixed side is the
caller's own value and the map key is the callee's, and `outputs.<name>` reads
the same way in the other direction.

**Rejected:** an implicit same-name copy from callee to caller, because the
caller's contract would then depend on a callee it does not name, and swapping
the callee would silently change what the caller returns. Also letting a later
stage read a callee's output in its mission text: that needs a stage-result
reference namespace this model does not define.

## Failure rules

- **Undeclared value.** The agent path cannot produce one — the tool exists
  only for a declared output. A Go stage that sets an undeclared key parks the
  run before its outcome transition, and a stage capture tool sharing a
  declared output's name is refused when the run starts.
- **Declared but never written.** With `required: true` the run parks naming
  the output and reaches no successful terminal state, so no caller observes a
  success missing a promised value. Without it the key is absent from the
  object rather than null, and a written `null` counts as not written, as
  `CheckInputs` treats a null input. A published output the callee never wrote
  leaves the caller's output absent, which the caller's own `required` then
  judges.
- **Save time.** `WorkflowInterface.Validate` refuses an unknown type and a
  malformed name. A call step's `outputs:` assignment is checked by a
  `checkCallOutputs` beside `checkCallInputs`: the key must be declared by the
  callee, the reference must name an output the caller declares, and the
  callee's declared type must satisfy the caller's.
- **Run time.** A captured value is re-validated against the declared type
  when the run reads it back, for the reason `readCaptures` re-checks every
  record: the captures file is writable by the harness user. A published
  callee value is re-checked against the caller's declared type before it
  enters the caller's set; a mismatch parks the caller naming the output.

**Rejected:** typing at save time only, because a captured value has no
save-time form; and at run time only, because a definition error would surface
as a failed run instead of a refused save.

## Storage and wire

- `tasks.outputs`, text, `NOT NULL DEFAULT ''`, written by the run's own row
  write — the shape `tasks.inputs` already has, with `EncodeOutputs` and
  `DecodeOutputs` beside `EncodeInputs`/`DecodeInputs`.
- The `Task` message carries it as `outputs_json`, because the container's
  `Update` sends the whole row and a column the message cannot carry is a
  column that write drops.
- `WorkflowCallStatusResponse` carries it as `outputs_json` beside `status`
  and `detail`, read from the callee's row by the handler that already
  verifies `call_parent_task_id`; `detail` keeps carrying the transition
  detail. The Go facade stays the two methods of `task.Caller`; no interface
  method is added.

**Forced by the grant:** a caller authenticates as its own task and may reach
`WorkflowCallStatus` on its own task ID, `Update`/`Transition`/`InsertEvent`
on its own row, and the step-execution pair on its own execution. It can read
neither the callee's row nor the callee's steps. `WorkflowCallStatus` is
therefore the only channel that exists, and it is already the sanctioned
cross-run read — this model fills the placeholder it carries. The values live
on the callee's row rather than on the caller's call step because the callee
writes them and the caller only receives them.

The write order is part of the contract: a run persists its outputs before its
outcome transition, so the status a caller sees when its wait ends already
carries them. A set whose encoded form passes the bound the store applies to a
structured payload (`clip`, as `ReviewPayload` is stored) is refused rather
than clipped, because clipped JSON does not parse.

No sentinel moves: a missing required output is an engine park reason, and a
caller asking about a task that is not its callee still gets `ErrCallNotYours`,
so nothing new crosses `mapError`/`unmapError` and no canonical message string
changes.

**Rejected:**
- A new read RPC (`WorkflowCallOutputs`) — a second poll of a row the caller
  already polls, whose answer could disagree with the status beside it, and a
  second grant-widening row for the same parent-child check.
- The step-execution record — `ListSteps` is granted by `read` on a run, so a
  caller cannot read the callee's steps at all, and making them readable would
  hand it the callee's whole step history, which the parent-child check exists
  to prevent. A step's `detail` is text clipped like every other detail.
- The caller's call step as the store — the value originates in another
  process and must outlive the container that produced it.

## Out of scope

- A run started by a binding: it has no caller, and its outputs live on its
  own row only.
- A stage reference to a previous stage's captured value.
- Assigning outputs in a caller cancelled while it waits.

## Verification

- A definition whose output type is not in the vocabulary, and a call
  publishing an output neither side declares, are refused when saved.
- A `wait: true` caller receives a value its callee wrote, returns it as its
  own output, and the caller's row carries it.
- A run whose required output is never written parks, and its caller's stage
  fails naming the output.
- A call step publishing an output over `wait: false` is refused when saved.
- A retried attempt that writes no outputs leaves an empty set on the row.
- An example under `examples/workflows/` carries an `outputs:` block and a
  call that publishes one, which `TestExampleWorkflowsParse` covers.
