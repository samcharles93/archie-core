# Control-plane apply status

**Status:** Draft
**Date:** 2026-09-21
**Tracking:** `archie-core-pskb`, and `archie-core-nwa0` which depends on it

## Outcome

An operator who edits a control-plane resource can see which processes are
running that version, which are still on an older one, and which rejected it.

`docs/prds/runtime-control-plane.md` requires this and does not say where the
report goes. This decides that.

## Where the report goes

Apply status is a State Store contract surface. `ControlPlaneService` is the
surface for editing a resource, and a status report is not an edit. The
precedent is the config snapshot pair: a process publishes what it knows, the
UI process reads it, and both RPCs are administrative, so a task-scoped grant
reaches neither. That denial needs no new rule: `authorizesTaskScopedCall`
names three methods and denies everything else.

`docs/prds/state-store-contract.md` gains the pair in its inventory, and its
RPC count at two places moves from 45 to 47.

The read returns all records as a list, including an empty one. The
`found=false` convention for absent single rows does not apply.

## What is reported

One record per process and resource kind, replaced in place. It holds the
process name, the resource kind, the version applied, the error if the process
could not apply it, and the time it reported.

A process reports at the point it applies a resource, so the record says what
happened rather than what was intended. Nothing probes.

Process names come from one shared constant naming `archied`, `archie-gateway`
and `archie-messaging`. A deployment runs at most one of each. Writer and
reader are bound to that constant by test, because a drifted name is
indistinguishable from a process that never reported.

## Which processes report

- `archied` and `archie-gateway` apply every restart-required kind through
  `boot.runtimeConfig`, at boot and on SIGHUP.
- `archied` also applies the live kinds it watches, and reports each update it
  applies.
- `archie-messaging` applies channel settings at startup and reports that kind.

## What can be reported, and what cannot

A process reaches its apply point only after the State Store has answered it.
When the State Store is unreachable, the process exits or refuses to start and
writes no record. An unreachable store is never reported as a failure by the
process it affected. A record carries the other failure: the store answered,
and the value would not validate.

Absence has two meanings and the UI never collapses them: no process applies
this kind, or a process that applies other kinds has not reported this one.

## Relationship to the reload status

Apply status covers control-plane resource kinds. `ReloadStatus` covers the
file document and stays the only report for a SIGHUP that failed to resolve or
validate it. Neither is derived from the other.

Writing a record is best-effort. On the SIGHUP path it takes its own bounded
context, separate from the budget the reload spends reading the control plane,
and a failed write is logged rather than turned into a failed reload.

## What the UI shows

The settings page shows, per resource, the stored version beside each process's
applied version, error, and report time. A process on an older version of a
restart-required resource is pending a restart. A process with an error is
failed, with the error beside it.

## Open question: a record outlives the process that wrote it

Records are replaced in place, so a process that applies a version and then
stops leaves a record asserting that version indefinitely. The settings page
would show a dead process as current. `docs/prds/status-health-surface.md`
faced the same choice and stamped the start of a poll pass so a wedged poller
reads as stale rather than healthy.

This is not settled here, because the fix is a mechanism the tracking issue
does not ask for. Three options:

1. Each process re-stamps its records on an interval, and a record that stops
   being re-stamped reads as unknown. Costs a recurring write per process.
2. Records carry the instance identity generated at process start, and the UI
   reads a changed identity as a restart. Detects restarts, not deaths.
3. Accept it. The page answers "what did each process apply" and not "is it
   still running", and says so.

Recommended: 1, because the question an operator brings to this page is whether
their change is live now.

## Verification

- A process that applies a resource writes a record carrying that version.
- A process whose apply fails validation writes a record carrying the error and
  no new version.
- A reload that fails leaves the previously applied version recorded.
- A record written under a process name outside the shared constant fails the
  test binding writer to reader.
- A task-scoped credential is refused by both RPCs.
- A failed report does not fail the reload that produced it.

## Not included

- History of past applies. Records are current state; resource history already
  records what was written.
- Checking a live replacement before switching to it. That is
  `archie-core-nwa0`, which reports its failures through this surface.
- Any change to how a resource is applied.
