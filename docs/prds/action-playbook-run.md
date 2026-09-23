# Running action playbooks

**Status:** Approved
**Date:** 2026-09-23
**Parent:** `docs/prds/multi-action-playbooks.md`, epic `archie-core-t2db`
**Beads issue:** `archie-core-t2db.31`

## Why this exists

An action playbook loads and type-checks but never runs: `Store.Dispatch`
skips it and the daemon logs a warning at boot (`multi-action-playbooks.md`,
D1). This document defines when it runs, what each action does, and how the
`playbook_dispatches` ledger gates it.

## When a playbook runs

The daemon runs action playbooks at the point it pins a task's workflow
definition (`Daemon.resolveWorkflowID`), using the same `DispatchInput` the
workflow playbooks use. The definition is pinned once per task, so the run
happens once per task.

- Every action playbook whose trigger matches runs, in load order. A workflow
  playbook stops at the first match because it chooses one workflow; action
  playbooks choose nothing, so no match suppresses another.
- A run never changes routing. The task is pinned by the workflow playbooks
  and bindings exactly as when no action playbook exists.
- An action or playbook failure never parks or fails the task. It is logged
  at warning level with the playbook, the action and the task.
- A task that already names a workflow (the approval requeue) is skipped, as
  it is for workflow playbooks.
- Actions run synchronously in the pin path, bounded by the daemon's context.
  The only Module kind, `log`, returns immediately. A slow kind must bring its
  own timeout.

## What one action does

Actions run in order against one evaluation context whose `actions` map
starts empty.

1. Evaluate `when`. False or an evaluation error skips the action (J3). A
   skipped action records nothing and produces no result.
2. Evaluate every `args` value. An evaluation error stops the playbook.
3. Call `playbook.InvokeOnce` with the Module invoke as its function. The
   ledger row is written first. A duplicate row skips the invoke.
4. An invoke error stops the playbook. The row stays, so the action is not
   retried: the ledger promises at most once, not at least once.
5. On success, `ModuleRegistry.DecodeResult` converts the result, and it is
   stored as `actions.<id>.result` when the action declares an id.

The event id in the ledger key is `DispatchInput.TaskID`, the issue's
idempotency key (`owner/repo/number`). An action therefore runs at most once
per issue for each playbook version. Editing a playbook changes its version
and lets it run again for issues it already handled.

A skipped duplicate has no result. A later action that reads it sees a
missing field: its `when` evaluates false, or its args fail and the playbook
stops. This only happens when the same issue is pinned twice under one
playbook version.

## Wiring

- `storecontract.PlaybookDispatcher` is lifted onto the daemon from the State
  Store client in `internal/app/archied/bootstrap.go`, the same way
  `BindingDispatcher` is.
- The daemon's `Playbooks` dependency gains the run method. The Module
  registry is passed to the playbook store at load, so the daemon needs no
  new dependency for it.
- With no `PlaybookDispatcher` wired, action playbooks do not run, and the
  boot warning names each one. They never run without the ledger.
- The boot warning that action playbooks do not execute is removed when the
  ledger is wired.

## Verification

- A matching two-action `log` playbook runs both actions in order, and the
  second reads the first's result.
- Pinning the same task twice under one playbook version invokes each action
  once.
- A `when` that evaluates false skips that action and the run continues.
- An invoke error stops the playbook. The task is still pinned to the same
  workflow it would get with no action playbook.
- A non-matching trigger runs nothing.
- With no ledger wired, nothing runs and the warning names the playbook.
