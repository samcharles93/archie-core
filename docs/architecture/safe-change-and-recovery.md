# Safe Change and Recovery

**Status:** Requirements approved; mechanics are under design. Database-backed settings are implemented through the control plane (2026-09): see the degrade/recovery section below, which records a gap rather than a solution. The full runtime-supervision protocol (bounded observation, versioned audit trail) remains under design.
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

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

## Database settings: degrade paths and recovery (2026-09)

The control plane owns runtime-tunable settings and stores them in `archie.db`.
This replaced a dashboard overlay store that degraded to file config on any
failure and had three documented ways back. The replacement has none, and the
posture is the opposite: **the daemon fails closed.**

### Degrade paths

| Failure | Boot behaviour | Where the operator sees it |
| --- | --- | --- |
| State Store unreachable | `boot.loadRuntimeConfig` returns the dial error and `archied` exits non-zero | stderr: `runtime settings unavailable` |
| A stored resource fails validation | Same: `validate database settings` wraps the error and the daemon exits | Same |
| SIGHUP reload with a bad file, or a control plane that cannot be read | Running config is kept; the reload records `last_error` / `last_error_at`. The reload re-applies the database layer, so it never publishes file values over the running database-owned ones | `/api/config` → `reload.last_error` → dashboard banner |

Failing closed is deliberate for settings the database owns: falling back to the
file values would restore the second source of truth the control plane exists to
remove. The cost is that a bad stored value stops the daemon starting, and the
value can only be corrected through the daemon's own API.

### Recovery: not yet built

There is no escape hatch. Nothing skips the control plane at boot, and the only
writer for a resource is `ControlPlaneService.Command` on a running State Store.
An operator whose stored settings will not validate has no supported path back
short of editing `archie.db` by hand.

`archie-core-6zw0` is the missing piece: offline commands that back up, restore,
validate and roll back the database without `archied` or the Web UI. Until it
lands, treat a settings change that requires a restart as a change that can
prevent one, and take a copy of `archie.db` first.

Resource history is the in-band remedy and does exist: every write records its
prior value, so an edit made through the dashboard or a channel can be restored
by replaying an earlier revision while the State Store is up.

## Gateway restart: two constraints learned the hard way

Telegram `/restart` (`internal/channels/telegram/restart.go` plus the `Start`
supervisor loop). Recovered 2026-08-09 from the pre-migration issue tracker.
Both constraints must survive any refactor.

**1. Deadlock.** The `/restart` handler runs *on* the bot instance the supervisor
is about to stop. Tearing down inline from the handler deadlocks the restart it
just asked for. So the handler sends on a **buffered** channel (`restartCh`,
capacity 1) and returns immediately; the supervisor loop in `Start` does the
teardown. Duplicate requests hit the default branch and are dropped, not queued.

**2. Lockout.** A failed config reload must **not** be fatal. It logs and
relaunches with the previous in-memory settings. If a bad config edit killed the
gateway, it would also destroy the only means of fixing it remotely — which is
the entire reason `/restart` exists.

**Scope is gateway-only by design.** The daemon keeps running so in-flight agent
tasks survive. Exiting the process would also restart under a `unless-stopped`
policy, but would kill running work. `Reload` (supplied by `cmd/archied/main.go`)
re-reads config from disk and refreshes **only** the token and `AllowedUserIDs`;
everything else is wired into the daemon at construction and still needs a full
restart.

**Authorisation is inherited:** `authorizedMessage` rejects non-allowlisted
senders before any handler runs, so all gateway commands are admin-only by
construction. Do not add per-command auth without checking that invariant still
holds.
