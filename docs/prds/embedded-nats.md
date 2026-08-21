# Embedded NATS -- decision

**Status:** Decided, not yet implemented
**Date:** 2026-08-22
**Beads issue:** `archie-core-7d5u.2`

`event-sources-and-reactions.md` settled the reaction-delivery mechanism (reuse
`internal/infrastructure/eventbus/nats`, a second stream under a fan-out
retention policy) and left the deployment shape to this issue. This document
decides that shape. `github.com/nats-io/nats-server/v2` is already a direct
dependency, used today only in `_test.go` files; the gap is that production
startup only knows "external URL configured" vs "no NATS, use SQLite
`ClaimNext`".

## 1. Embedded runs alongside SQLite, not instead of it

The SQLite store remains authoritative for task state. NATS -- external or
embedded -- is the *distribution* path, and `drainNATS` already falls back to
`Store.ClaimNext` for requeued tasks (waiting_human approval, retry-parked)
that never went through a NATS publish. So wiring embedded NATS does not
remove the SQLite flow; it makes `Daemon.Tasks != nil` true in single-process
deployments, which routes poll→enqueue through `pollNATS` and drain through
`drainNATS` exactly as an external-NATS deployment already does. `ClaimNext`
stays as the requeue fallback inside `drainNATS`. No new drain path, no second
eventbus type.

## 2. Config: `[nats] mode = "embedded" | "external" | "off"`

`NATSConfig` gains a `mode` field. `url` no longer doubles as the on/off
switch -- that coupling is what made "no NATS" a permanent stance instead of a
deployment shape.

| mode       | meaning                                                        |
|------------|----------------------------------------------------------------|
| `external` | connect to `url` (today's behaviour). `url` is required.       |
| `embedded` | start an in-process nats-server; the client connects to it.    |
| `off`      | no NATS at all; the old SQLite-only flow (`ClaimNext`).        |

Default resolution: `url` set → `external`; `url` empty and `mode` unset or
`embedded` → `embedded`; `mode = "off"` → `off`. `mode = "external"` with no
`url` is a validation error; `mode = "off"`/`"embedded"` with a `url` set is a
validation error. This is a meaning change and is stated as one: `url` empty
*used to mean* "no NATS, SQLite only"; it *now means* "embedded NATS", and
getting the old behaviour requires the explicit opt-out `mode = "off"`.

`agent.mode = "nats"` and `[containers]` require `mode = "external"` (a
container cannot reach an in-process loopback server, and embedded NATS is not
an agent-execution topology).

## 3. The daemon owns the embedded server's lifecycle

`internal/infrastructure/eventbus/nats` gains an `EmbeddedServer` type that
wraps `nats-server/v2/server.Server`, exposing only `ClientURL()` and
`Shutdown()`. The composition root (`cmd/archied/bootstrap.go`) starts it in
`connectNATS` (before dialing the client), binds it to `127.0.0.1` on a random
port, enables JetStream with a store dir under the daemon's data directory
(`<dir of DBPath>/nats`), and registers a cleanup that shuts the server down
*after* the client closes. Cleanup ordering is LIFO: the server shutdown is
registered before the client close, so the client closes first.

## Retention is a Config field, not a constant

`nats.Config` gains `Retention *jetstream.RetentionPolicy`, defaulting to
`jetstream.WorkQueuePolicy` in `withDefaults`, replacing the hardcoded value in
`Connect` (`client.go:65`). `WorkQueuePolicy` is correct for `ARCHIE_TASKS`
(one consumer claims each message) and wrong for reactions (fan-out). This is
the enabler for the second reaction stream: `event-sources-and-reactions.md`
requires it, and nothing here creates the reaction stream itself -- that is
provisioned by the beads that build reaction producers and consumers, which do
not exist yet. A test proves the parameterization: two overlapping `Subscribe`
consumers both receive a message under `LimitsPolicy` (fan-out) and starve
under `WorkQueuePolicy`, the exact trap `CLAUDE.md` documents.

## Packages this touches

- `internal/infrastructure/eventbus/nats`: `Retention` on `Config`, an
  `EmbeddedServer` type.
- `internal/config`: `NATSConfig.Mode`.
- `internal/infrastructure/configuration`: `mode` defaulting and validation.
- `cmd/archied/bootstrap.go`: start/stop the embedded server; set
  `Daemon.ConnectedNATS.URL` to the embedded client URL so container env (if
  ever enabled) never receives an empty endpoint.
- `config.example.toml`: document `mode`. The `deployments/*` profiles are
  unchanged on purpose: their absent `[nats]` section now selects embedded by
  default, which is the intended single-process behaviour this issue exists to
  provide, not a regression to paper over with `mode = "off"`.
