# Control-plane apply status

**Status:** Draft
**Date:** 2026-09-21
**Tracking:** `archie-core-pskb`, and `archie-core-nwa0` which depends on it

## Outcome

An operator who edits a control-plane resource can see which processes are
running that version, which are still on an older one, which rejected it, and
which have stopped saying anything at all.

`docs/prds/runtime-control-plane.md` requires the first three ("Each process
reports the version it is using or why it failed. The UI shows both") and does
not say where the report goes. This decides that, and adds the fourth, because
without it a process that dies after a successful report keeps asserting
health.

## Where the report goes

Apply status is a State Store contract surface. `ControlPlaneService` is the
surface for editing a resource, and a status report is not an edit. The
matching precedent is the config snapshot pair: a process publishes what it
knows, the UI process reads it, and both RPCs are administrative, so a
task-scoped grant reaches neither. That denial needs no new rule;
`authorizesTaskScopedCall` names three methods and denies everything else.

`docs/prds/state-store-contract.md` gains the pair in its inventory, and its
RPC count at two places moves from 45 to 47.

The read returns every record as a list, including an empty one. It is not a
singleton read, so the `found=false` convention for absent single rows does not
apply to it.

## What is reported

One record per process instance and resource kind, replaced in place. It holds:

- the process name and the instance identity it generated at startup;
- the resource kind;
- the version the process applied, and the error if it could not;
- the time it last reported.

A process reports at the point it applies a resource, so the record says what
happened rather than what was intended. Nothing probes.

Each reporting process re-stamps its records on a fixed interval. The report
time is therefore a liveness signal, not just a write timestamp: a record that
has stopped being re-stamped belongs to a process that has stopped running.

Process names come from one shared constant naming `archied`, `archie-gateway`
and `archie-messaging`. A deployment runs at most one instance of each. A
producer that invents its own name would be indistinguishable from a process
that is down, so writer and reader are bound to that constant by test.

## Which processes report

- `archied` and `archie-gateway` apply every restart-required kind through
  `boot.runtimeConfig`, at boot and on SIGHUP.
- `archied` also applies the live kinds it watches, and reports each update it
  applies.
- `archie-messaging` applies channel settings at startup and reports that kind.

## What can be reported, and what cannot

A process reaches its apply point only after the State Store has answered it.
When the State Store is unreachable, the process exits or refuses to start, and
writes no record at all. An unreachable store is therefore never reported as a
failure by the process it affected. What a record can carry is the other
failure: the store answered, and the value it returned would not validate.

The UI distinguishes three absences, and never collapses them:

- no process applies this kind, so there is no record and none is expected;
- a process has records for other kinds but none for this one;
- a process has a record that has gone stale, meaning it is no longer running.

## Relationship to the reload status

Apply status covers control-plane resource kinds. `ReloadStatus` covers the
file document, and stays the only report for a SIGHUP that failed to resolve or
validate the file. The two are not merged and neither is derived from the
other.

Writing an apply-status record is best-effort everywhere, and on the SIGHUP
path specifically it gets its own bounded context, separate from the budget the
reload spends reading the control plane. A failed report is logged and never
turned into a failed reload: the configuration it describes has already been
applied, and reporting it badly must not raise a banner saying the reload
failed.

## What the UI shows

The settings page shows, per resource, the stored version beside each process's
applied version, error, and how recently it reported. A process on an older
version of a restart-required resource is pending a restart. A process with an
error is failed, with the error beside it. A process whose record has gone
stale is unknown, never current.

## Verification

- A process that applies a resource writes a record carrying that version and
  its instance identity.
- A process whose apply fails validation writes a record carrying the error and
  no new version.
- A reload that fails leaves the previously applied version recorded.
- A record that stops being re-stamped renders as unknown rather than current.
- A record written under a process name outside the shared constant fails the
  test that binds writer to reader.
- A task-scoped credential is refused by both RPCs.
- A failed report does not fail the reload that produced it.

## Not included

- History of past applies. Records are current state; resource history already
  records what was written.
- Checking a live replacement before switching to it. That is
  `archie-core-nwa0`, which reports its failures through this surface.
- Any change to how a resource is applied.
