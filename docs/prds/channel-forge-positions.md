# Channel and Forge action positions (resolves `t2db.19`)

**Status:** Approved
**Date:** 2026-09-03
**Parent:** `docs/prds/eda-playbook-engine.md`, epic `archie-core-t2db`
**Blocked on:** `archie-core-t2db.24` (the `playbook_dispatches` ledger) for
implementation of either position. Both are side-effecting, and `t2db.17`
resolved the keying scheme in design only.

## Forge

`internal/forge.Forge` is already exactly the shape a native-Go action position
needs -- a real, typed, general-purpose interface, not something that needs a
Yaegi/Module-style schema-generation detour:

```go
type Forge interface {
    IssueForge        // AssignedIssues, IssuesWithLabel, CloseIssue, ...
    PullRequestForge  // CreatePR, PRState
    RepoForge         // AcceptInvitations, VerifyPush, LinkBranch, ...
}
```

**Typed contract shape: native-Go-by-name, not a Module-kind.** Module's whole
reason to exist is giving _Yaegi-authored, operator-supplied_ code a typed
contract to implement. `Forge` is already a typed Go interface with real, tested
implementations (GitHub, Gitea) selected at daemon startup by `forge.New(...)`.
Wrapping it in a Module-kind schema/`go:generate` detour would duplicate a
contract that already exists. A Forge action position should be: a small set of
named operations (e.g. `close-issue`, `create-pr`, `link-branch`), each with a
typed `Args`/`Result` struct mirroring the matching method's parameters/return.

The dispatch target is `internal/domain/workflow.Forger`
(`CloseIssue`/`CreatePR`/`LinkBranch` -- already exists, already exactly these
three operations), **not** `internal/forge.Forge` directly. `organisation.md`'s
target structure places `forge/` under `internal/infrastructure/forge/`; the flat
`internal/forge` is a flat, unmigrated legacy package in the same position
`internal/workflow` was in before its own migration. A new domain package
depending on the flat package directly would create a domain-to-infrastructure
dependency that breaks the moment `forge` migrates. `workflow.Forger` is the
narrow, already-domain-owned contract that exists for exactly this reason -- the
daemon's real `forge.Forge` client satisfies it at the wiring layer.
Depend on `Forger`, not on `internal/forge`.

**Operation scope for a first slice:** `close-issue` and `link-branch`
(single-call, cleanly idempotent by event+issue identity) are the safe starting
set. `create-pr` is explicitly a heavier case -- opening a PR is a
harder-to-reverse action with its own dedup shape beyond "did this event fire
before" (a redelivered event must not open a second PR for the same logical
change) -- defer it to a follow-up slice once the keying scheme is proven on the
simpler operations, don't design it blind now.

**Host access:** the coordinator needs the same `Forge` instance
`internal/daemon`'s poller already holds (constructed once at startup via
`forge.New`). This is a daemon-composition wiring question (pass the existing
instance into the coordinator's constructor), not a new extensibility mechanism.

## Channel

### Constraints this satisfies

Messaging is the root, Channel is the integration layer, an implementation such
as Telegram sits under Channel. A channel is added by implementing one reusable
interface, and every channel must implement it. A notification mechanism belongs
under Messaging, and `channels.Channel` must not reach across into it.

### Notification is a Messaging contract, not a channel type

The contract lives in `internal/domain/messaging`. A channel implementation
satisfies it by importing its own parent, which it already does, so the
dependency stays `channels -> messaging` and nothing points back.

`channels.Channel` does not change. It gains no notification method and no
import, so the interface every channel must implement stays the lifecycle and
config contract it is. `internal/channels/telegram` satisfying both
contracts is an implementation choosing to, not the Channel layer knowing about
notification.

A notification is not a channel type because it has no lifecycle to start and
stop, no inbound direction, and no configuration schema of its own. Modelling it
as one would force `Start`/`Stop`/`ConfigSchema` onto something with no meaning
for them, and make every real channel declare whether it is a notification.

### Destinations are configured, never named by a playbook

`[notify]` becomes a set of named destinations. Each binds a name the operator
chooses to a channel already registered in the Messaging composition
(`internal/app/archiemessaging/compose.go` registers `telegram`, `email`,
`webhook`) plus that channel's own address, such as a Telegram chat id. The
existing single-webhook form keeps working as a destination named `webhook`.

A playbook action names a destination. It never names an address. Playbook
triggers are reachable from a public, unauthenticated intake surface, so an
action that could address an arbitrary chat would be a new exfiltration surface;
a destination the operator configured once is not.

### Args and Result

```go
type NotifyArgs struct {
    Destination string // a name from [notify]
    Text        string
}

type NotifyResult struct {
    Delivered bool
}
```

An unknown destination is a reported error at playbook load, not at dispatch:
the destination set is known at startup, so this follows the engine's
reject-at-load rule.

### Host access: no new process surface

`archie-messaging` is its own process (`cmd/archie-messaging`). It dials out to
the Gateway and the State Store and serves nothing, and it deliberately has no
NATS connection: `internal/app/archiemessaging/telegram_features.go` records
that it "must not decode" the daemon's `[nats]` config. So a notification cannot
be pushed into it by any route.

Delivery therefore travels the connection that already exists. Every process
that needs Messaging already dials the Gateway: `archied`
(`internal/app/archied/chat_service.go`), `archie-messaging`
(`internal/app/archiemessaging/run.go`) and `archie-ui`. The Gateway is the hub.
A new server-streaming RPC on `ChatService` lets `archie-messaging` subscribe at
startup and receive notifications the daemon publishes; Messaging then delivers
through the named channel instance it already holds in `Service.channels`. No
new listener, no NATS in Messaging, no change to how any process is reached.

The existing `Stream` RPC is not reused: it streams one chat turn's events for
one inbound message (`internal/infrastructure/gatewayrpc/server.go:Stream`).

### Out of scope

Giving playbook-originated sends the session and approval scoping the
interactive channels use. Configured destinations carry the address, so there is
no conversation to anchor to and no scoping mechanism to generalize.

## Packages this touches

- `internal/domain/eda` (or a new sibling package, e.g.
  `internal/domain/eda/forgeaction`): typed `Args`/`Result` per operation, a
  small registry keyed by operation name, dispatched against a
  `workflow.Forger`-typed value -- not `internal/forge.Forge` directly (see
  correction above).
- `internal/app/archied`: wiring the coordinator's constructor to receive the
  daemon's already-built `forge.Forge` client, passed in as the
  `workflow.Forger` it already satisfies -- no new lifecycle to manage, and no
  change to `internal/forge` itself.

- `internal/domain/messaging`: the notification contract and its
  `NotifyArgs`/`NotifyResult`.
- `internal/config`: `[notify]` becomes named destinations.
- `proto/gateway/v1` and `internal/infrastructure/gatewayrpc`: the
  server-streaming notification RPC.
- `internal/app/archiemessaging`: subscribe at startup, resolve a destination to
  a registered channel instance, deliver.
