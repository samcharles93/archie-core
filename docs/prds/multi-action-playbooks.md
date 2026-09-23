# Multi-action playbooks and typed action results

**Status:** Approved
**Date:** 2026-09-22
**Parent:** `docs/prds/eda-playbook-engine.md`, epic `archie-core-t2db`
**Beads issue:** `archie-core-nx30` (unblocks `archie-core-t2db.27` and `archie-core-t2db.31`)

## Why this exists

`eda-playbook-engine.md`'s hard boundary rejects any playbook that is not
exactly one `workflow` action, so the parent document's data-flow, Module and
Channel/Forge decisions have no loadable shape to attach to. The boundary's own
stated condition is now met: both execution-time gaps are resolved
(`eda-playbook-engine.md`, "Execution-time gaps"; the `playbook_dispatches`
ledger). This document designs the relaxation.

## A playbook is one of two shapes

- A **workflow playbook** is exactly one `workflow` action. Unchanged: it names
  a workflow definition and is routed by the daemon's definition pin.
- An **action playbook** is one or more `module` actions in order. Each action
  has an optional `id`, a `kind` naming a registered Module kind, `args`, and an
  optional `when`.

One playbook does not mix the two shapes.

```yaml
trigger: { kind: bug }
actions:
  - id: build
    position: module
    kind: log
    args: { message: '"build started"' }
  - id: done
    position: module
    kind: log
    args: { message: '"done: " + string(actions.build.result.written)' }
    when: actions.build.result.written == true
```

The first Module kind is the side-effect-free `log` kind
(`module-position.md`, "Recommended first slice"). A Module kind with an
external side effect, and the Channel and Forge positions, are out of scope.

## A result type is the kind's generated Result struct

Each Module kind owns a hand-written `Args` and `Result` struct
(`internal/domain/eda/module/log` is the worked example); `ModuleRegistry.Invoke`
returns the result as a flat `map[string]any`. `ModuleRegistry.DecodeResult`
(`internal/domain/eda/module`) is the single site that converts that map into
the kind's `Result` struct before a later expression reads it, so the value the
checker validates and the value the run reads have one definition.

`actions.<id>` is typed by the kind of the action that declares `<id>`:
`{ result: <KindResult> }`. `event` stays `dyn` (J4,
`playbook-expression-syntax.md`).

## Reject-at-load, preserved

Relaxing the boundary must not make a bad reference resolvable. The load fails,
naming the playbook and the offending action, for:

- an `id` missing where a later action references it, duplicated, or outside
  its shape's grammar: a workflow action id keeps the shared
  stable-identifier grammar (J1, already enforced), while a module action id
  must be a lowercase CEL field name writable as `actions.<id>` (so a CEL
  keyword such as `in`, `true`, `false`, or `null` is rejected at load);
- an `actions.<id>` read of an id no earlier action declares;
- an `actions` read that is not statically resolvable to an id (any map index,
  literal or dynamic, `in`, a comprehension): the typed `actions` root is an
  object type, so only field selection `actions.<id>` resolves and the
  `actions["id"]` spelling is not accepted;
- a `result` field the referenced kind's `Result` does not define;
- a reference to an earlier action that produces no result;
- an unknown `position`, an unknown `kind`, or a `module` action with no `kind`;
- a field belonging to the other shape (`kind` on a `workflow` action, or
  `workflow` on a `module` action);
- an arg KEY the kind's `Args` schema does not define. Arg VALUE type-checking
  is tracked as `archie-core-t2db` debt, not shipped here.

Declaring the kind's `Result` schema to CEL at load makes a result-field typo a
load failure rather than a dispatch failure. Matching arg keys against the
kind's `Args` schema makes an arg-key typo a load failure; arg value
type-checking is tracked as `archie-core-t2db` debt.

## The environment is per playbook

Result typing depends on the ids a playbook declares and the kind each id runs,
so the CEL environment is built from one playbook's action list rather than once
per `Store`, and `expr.NewEnv` takes that list: each declared id becomes a field
of the `actions` object typed by its kind's `Result`.

## Running an action playbook is out of scope

This document makes an action playbook load and its references type-check.
Running one is designed in `docs/prds/action-playbook-run.md`.

## Decisions

**D1 — dispatch before `t2db.31`.** Superseded by
`docs/prds/action-playbook-run.md`. Was: an action playbook loads and
validates but is not routed; the definition-pin dispatch reports no match for it,
and the daemon logs a visible warning naming it.

**D2 — mixing positions.** Settled: one shape per playbook. A `workflow` action
yields a task, not a result, so it cannot feed `actions.<id>.result`.

**D3 — the CEL typing mechanism.** Settled: a per-playbook environment whose
`actions` root is an object type with one field per declared id, each field typed
by the kind's `Result` struct. `internal/domain/eda/expr` implements it with the
generic `actionResultProvider`.

**D4 — `args` against the kind's `Args` schema.** Settled: arg KEY checking ships
now. Arg value type-checking is tracked as `archie-core-t2db` debt.

**D5 — where the result map becomes the `Result` struct.** Settled:
`ModuleRegistry` stays schema-agnostic. `ModuleRegistry.DecodeResult`
(`internal/domain/eda/module`) is the conversion site.

## Verification

- A two-action `module` playbook with a valid `actions.<id>.result.<field>` read
  loads; the same read of a field the kind's `Result` does not define fails the
  load naming the field; a read of an id no earlier action declares fails; a
  dynamically indexed `actions` read fails.
- An arg KEY the kind's `Args` schema does not define fails the load. Arg VALUE
  type-checking is tracked as `archie-core-t2db` debt.
- The one-action `workflow` playbook shape, its dispatch, and its existing
  load-time checks are unchanged.
