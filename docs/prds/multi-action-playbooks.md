# Multi-action playbooks and typed action results

**Status:** Draft
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
returns the result as `map[string]any`. The run marshals that map into the
kind's `Result` before a later expression reads it, so the value the checker
validates and the value the run reads have one definition.

`actions.<id>` is typed by the kind of the action that declares `<id>`:
`{ result: <KindResult> }`. `event` stays `dyn` (J4,
`playbook-expression-syntax.md`).

## Reject-at-load, preserved

Relaxing the boundary must not make a bad reference resolvable. The load fails,
naming the playbook and the offending action, for:

- an `id` missing where a later action references it, duplicated, or not a
  stable identifier (J1, already enforced);
- an `actions.<id>` read of an id no earlier action declares;
- an `actions` read that is not statically resolvable to an id (a dynamic
  index, `in`, a comprehension — already enforced);
- a `result` field the referenced kind's `Result` does not define;
- a reference to an earlier action that produces no result;
- an unknown `position`, an unknown `kind`, or a `module` action with no `kind`;
- `args` that do not type-check against the kind's `Args` schema, or an arg key
  the schema does not define.

The last two are new. Declaring the kind's `Args` and `Result` schemas to CEL at
load makes an arg or result typo a load failure rather than a dispatch failure,
which is the field-level rejection `playbook-expression-syntax.md` chose CEL for.

## The environment is per playbook

Result typing depends on the ids a playbook declares and the kind each id runs,
so the CEL environment is built from one playbook's action list rather than once
per `Store`. `expr.NewEnv()` declares `actions` as `map(string, dyn)` and takes no
parameters, so its signature changes to accept the declared ids and their result
types.

## Running an action playbook is out of scope

This document makes an action playbook load and its references type-check. It
does not invoke a Module kind. Invocation, the `playbook_dispatches` gate, and
stop-on-first-failure across actions are `archie-core-t2db.31`.

## Decisions required

**D1 — dispatch before `t2db.31`.** An action playbook has no run path yet.
Either the definition-pin dispatch considers only workflow playbooks and reports
no match for an action playbook (loaded and validated, not routed), or the loader
refuses an action playbook until `t2db.31` lands. The first ships the loader and
the checker now but leaves a playbook that does nothing; the second keeps
"loadable means runnable" but cannot ship this work alone. Recommendation: the
first, with the daemon logging once that an action playbook has no run path.

**D2 — mixing positions.** May one playbook mix `workflow` and `module` actions?
A `workflow` action yields a task, not a result, so it cannot feed
`actions.<id>.result`, and its "invoke" is the definition pin rather than an
action call. Recommendation: one shape per playbook for this slice.

**D3 — the CEL typing mechanism.** Two candidates: build a per-playbook
environment whose `actions` root is an object type with one field per declared
id, each field typed by the kind's `Result` (cel-go native or provider-defined
types); or keep `actions` as `map(string, dyn)` and validate every static
`actions.<id>.result.<field>` path after compiling against an id-to-result-fields
map, in the same AST walk that already classifies `actions` references. The
first is CEL's own checker and costs a per-playbook environment; the second
keeps one environment and one compile path but checks fields outside CEL.
Recommendation: the first if cel-go can declare the ids' types without a custom
`ref.Val` adapter; otherwise the second, which is a bounded path check over a
schema map, not the re-implemented type system the parent document rejected.

**D4 — `args` against the kind's `Args` schema.** This document assumes the kind's
`Args` schema is declared to CEL as well, so an arg typo or wrong type fails the
load. Confirm, or split that check into its own bead.

**D5 — where the result map becomes the `Result` struct.** `ModuleRegistry.Invoke`
returns `map[string]any`. Confirm the marshalling site: the coordinator, the
registry, or the kind itself.

## Verification

- A two-action `module` playbook with a valid `actions.<id>.result.<field>` read
  loads; the same read of a field the kind's `Result` does not define fails the
  load naming the field; a read of an id no earlier action declares fails; a
  dynamically indexed `actions` read fails.
- An `args` key or value the kind's `Args` schema rejects fails the load.
- The one-action `workflow` playbook shape, its dispatch, and its existing
  load-time checks are unchanged.
