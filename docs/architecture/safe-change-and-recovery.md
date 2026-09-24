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

The control plane owns runtime-tunable settings and stores them in the State
Store's PostgreSQL database.
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

### Upgrading an install from SQLite

The State Store, the daemon and the Gateway serve from `database_url` only. An
install that still holds the SQLite stores (`<db_path>-tasks.sqlite`,
`<db_path>-eda.sqlite`, `<db_path>-conversations.sqlite`) upgrades in three
steps:

1. Stop every archie process.
2. Run `archie-state-store import`. It moves all three stores into Postgres in
   one transaction, verifies the rows, and records completion. The SQLite files
   are read, never written.
3. Start archie.

`db_path` exists only to locate these files for the import and the boot gate
below, and goes with the importer. The embedded NATS store, its endpoint file
and task logs live under `state_dir` (default: the data home's `archie`
directory). An install whose `db_path` sat outside the data home sets
`state_dir` to that directory to keep them where they were.

Each serving process checks this at boot, after migrating the schema. With no
completion record and a non-empty legacy file it refuses to start and names the
files and the import command, rather than serve empty tables over existing
data. There is no override. With no legacy data it is a fresh install: it writes
the completion record itself, so a legacy file that appears later is ignored.
A database that recorded a fresh start refuses a later import.

The Gateway holds a Postgres serve-ownership claim for its whole life, so a
second Gateway against the same database refuses to start (its turn recovery
would fail the first one's in-flight turns). The State Store holds its own.

### Recovery: offline commands on the State Store

Nothing skips the control plane at boot, and the only writer for a resource is
`ControlPlaneService.Command` on a running State Store -- which is exactly what
the daemon fails closed without. `archie-state-store` therefore carries the
offline recovery commands (`archie-core-6zw0`). They work on the PostgreSQL
database the configuration's `database_url` names
(`internal/app/archied/state_store_recovery_postgres.go`,
`internal/infrastructure/postgres/maintenance.go`) and need neither `archied`
nor the Web UI running: `validate` reads the configuration file the daemon
boots with, which is what makes its verdict the daemon's verdict. One database
backs every service, so the scope is always the whole database.

| Command | What it does | Serving processes |
| --- | --- | --- |
| `backup -out F` | runs `pg_dump --format=custom`, reads the archive back with `pg_restore --list` and only then replaces `F` | may be running |
| `restore -from F` | drops tables the snapshot does not hold and runs `pg_restore --clean --single-transaction`; every write made after the snapshot is lost | every one must be stopped |
| `validate [-config C]` | refuses a schema newer than the binary's highest migration; every stored resource decodes under the definition that owns it; and `configuration.Validate` accepts the settings the daemon would read -- the stored ones, or the seed the State Store writes for a kind the database does not hold yet -- layered onto `C`, the check the daemon reports as `validate database settings` | may be running |
| `rollback -kind K [-revision N]` | replays an earlier revision of a stored resource through the ordinary replace, recording the rollback as a new revision | the State Store must be stopped |

The two validation layers `validate` reports are not the same check, and it
reports both rather than merging them: the write path's own decode is stricter
about shape (it rejects unknown fields boot's decode ignores), while boot is
stricter about meaning (only boot checks `dispatch.trigger`, and a positive
`poll_interval`). Either one refusing is a store somebody has to look at.

No rollback RPC exists and none is needed: a revision carries its own value, so
restoring one is the same replace the dashboard and the channels already use.
The offline command is that operation with the store stopped -- the one state in
which the daemon's own API cannot serve it.

"Must be stopped" is checked rather than assumed. Each serving process holds a
Postgres ownership lock for its whole life. `rollback` holds the `state-store`
claim for the replay. `restore` claims every service role's lock (`state-store`
and `gateway`) without waiting and refuses if any is held or the check cannot
complete. The read-only commands -- `backup` and `validate` -- take no claim and
are meant to run against a serving database: `backup` because the update
installer runs inside the process it is replacing and cannot stop the State
Store, and `validate` because an operator checks a running deployment with it.
`scripts/archie-update-install` takes its pre-flight snapshot through this
subcommand, so the installer and an operator run one implementation.

`rollback` is the surgical route back from a bad stored value; `restore` is the
blunt one, and is also the way back from a schema migration a release cannot
grow out of.

There is no automatic schema rollback. goose `Down` drops tables, so it is data
loss, not a rollback. `scripts/archie-update-install` refuses a daemon update
it cannot snapshot first, and on a failed update `scripts/archie-update-watchdog`
rolls the binaries back, leaves the database alone and prints the `restore`
command for the pre-update snapshot. Whether to run it is the operator's call.

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
