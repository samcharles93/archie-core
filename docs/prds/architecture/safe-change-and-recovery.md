# Safe Change and Recovery

**Status:** Requirements approved; mechanics are under design  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Purpose

Archie is built and improved by agents as well as humans. The architecture MUST
make a change easy to locate, understand, test, audit, extend, and reverse
without requiring its author to understand unrelated parts of the application.

Separation of concerns is therefore an operational safety boundary, not only a
code-organisation preference.

## Required outcomes

- An agent can improve a focused feature without receiving authority over
  unrelated domains.
- Every event, function, action, setting, and policy has an identifiable owner
  and provenance.
- Source and runtime changes pass deterministic checks outside the actor making
  the change.
- A rejected or unhealthy candidate cannot replace the last known working
  version.
- Invalid user configuration does not terminate an otherwise healthy
  application.
- Configuration and service failures are observable, attributed, and eligible
  for bounded remediation or rollback.
- The application continues serving unaffected capabilities when one
  capability is misconfigured or unhealthy.
- Recovery failure is reported and contained rather than hidden in an
  unbounded retry loop.

## Source changes

Agent-authored source changes occur through the task-execution boundary in an
isolated workspace. The agent does not directly mutate the running
installation.

Promotion is controlled by deterministic repository-owned policy, including
tests, generated-reference drift checks, protected paths, change scope, and
other required gates. The actor proposing a change cannot waive these checks.

Repository and domain separation SHOULD allow a task to load and modify only
the governing requirements and code for its declared scope.

## Runtime changes

Configuration and other runtime changes are proposed as candidates rather than
mutating the active state in place.

The affected domain owns:

- the command describing the semantic change;
- validation of its typed settings;
- health signals that demonstrate correct operation;
- domain-specific remediation and rollback consequences.

Shared policy supplies consistent evaluation, evidence, and audit mechanics.
Identity supplies actor attribution. Infrastructure supplies external
configuration storage, service control, and other operational adapters.

## Runtime change protocol

Last-known-good preservation and candidate promotion are the universal runtime
change protocol.

1. An identity proposes an immutable candidate version.
2. Infrastructure parses it without changing active state.
3. Each affected domain validates its typed settings.
4. Shared policy evaluates cross-cutting constraints.
5. Each affected capability prepares the candidate without destroying its
   active instance.
6. Capability-specific health checks prove the candidate works.
7. Runtime supervision atomically promotes the candidate.
8. A bounded observation period watches for delayed failure.
9. Failure restores the last-known-good version and records the reason,
   evidence, actor, and involved versions.
10. Repeated remediation failure stops retrying, isolates the capability, and
    reports the fault.

The affected domain owns the semantic change command, typed validation, health
signals, and remediation consequences. Shared runtime supervision owns the
promotion transaction. Infrastructure performs storage and service-control
operations.

The protocol's exact Go contracts, storage model, atomicity boundary, and
observation rules require a focused runtime-supervision design pass.

## Failure isolation

Invalid input MUST leave the active valid configuration unchanged and produce a
diagnostic record.

A capability that cannot start or reload SHOULD fail independently where its
absence does not violate a declared application invariant. Unaffected
capabilities continue operating.

Remediation MUST be bounded, observable, and policy-driven. Recovery MUST NOT
silently oscillate between versions or retry forever.
