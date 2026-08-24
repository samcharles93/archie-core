# Embedded NATS execution -- decision

**Status:** Amended; implementation in progress

**Date:** 2026-08-24

**Beads epic:** `archie-core-q15y`

This decision supersedes the 2026-08-22 position that embedded NATS was only a
distribution mechanism and could not be an agent-execution topology. That was
true only because the embedded listener was unauthenticated loopback. It left a
second production architecture in which `archied` ran autonomous workflows in
its own process, weakening the isolation boundary that managed task containers
exist to provide.

## One execution architecture, two broker deployments

Every autonomous repository workflow is handed over as one complete task via
NATS and runs in a task-scoped `archie-agent` container. `archied` remains a
native, long-lived orchestration and interactive-chat process; its operator
configured host filesystem access is unchanged. It owns task admission,
state, forge and worktree operations, container lifecycle, and the broker when
the embedded deployment is selected. It does not run an autonomous model loop.

NATS has two supported deployment shapes:

| mode | meaning |
| --- | --- |
| `embedded` | `archied` owns an in-process JetStream server (default). |
| `external` | `archied` and workers connect to the configured NATS URL. |

Both shapes have identical task semantics: one container and one full-task
request per workflow. Broker placement must not select a different executor.
The legacy `nats.mode = "off"`, `agent.mode = "inprocess"`, per-stage NATS, and
subprocess execution paths are migration inputs only and will be removed rather
than retained as production fallbacks.

SQLite remains authoritative for task state. NATS is the sole autonomous work
handoff. SQLite recovery may identify work that needs republishing, but it must
not cause `archied` to call `workflow.Run` locally.

## Embedded reachability and authentication

An embedded listener uses a cryptographically random bearer token generated for
each daemon start. The token exists only in process memory and the environment
of managed worker containers; it is never written to config or logs.

For a native daemon with managed containers, startup resolves the Docker bridge
the workers will join. The listener binds only to that bridge's host gateway
address on a random port. Both the native daemon and workers use that same
reachable endpoint, and every client supplies the generated token. Binding a
Docker bridge address keeps the listener off ordinary LAN interfaces without
publishing a host port. A configured user bridge is respected; otherwise the
Docker default bridge is used. A network without a usable local IPv4 gateway is
a startup error for autonomous execution.

If Docker is unavailable, interactive chat may still start and embedded NATS
may remain loopback-only, but autonomous tasks park with an explicit container
capability failure. They never fall back to host execution.

## Ownership and lifecycle

- `internal/infrastructure/eventbus/nats` owns the authenticated embedded
  server and exposes its runtime endpoint, not the server implementation.
- `internal/container` resolves the worker bridge and gateway and owns task
  container lifecycle.
- `internal/app` composition selects embedded or external broker deployment,
  freezes the connected runtime endpoint for worker environments, and wires the
  full-task handoff.
- `cmd/archied` remains process input only as the domain migration reaches this
  composition path.

The daemon starts the embedded server before its client, closes clients before
the server, and keeps JetStream data beneath the daemon data directory. NATS URL
and token are startup-built values: a reload cannot send new workers to a broker
different from the one the daemon is using.
