# Playbook bindings: model and threat model

**Status:** Approved foundation (documents shipped code: `archie-core-t2db.1`
through `.5`, `.7`, `.9`-`.15`)
**Date:** 2026-09-03

This document covers the binding model and the intake threat model portion
of `archie-core-t2db.6`. It does not cover the `ARCHITECTURE.md` task-lifecycle
addendum or the operator walkthrough -- those remain separate, open pieces of
the same ticket.

## What a binding is

A `binding` (`internal/domain/binding.Binding`) ties four things together:
a **matcher** (which captured events it applies to), a **mapping**
(`internal/domain/mapping.Mapping`, how fields are pulled out of a payload),
a **workflow** (which registered workflow runs on a match), and a **state
machine** governing whether it is live.

```go
type Binding struct {
    ID, Name           // identity
    Matcher            // { Source string } -- the webhook path segment, e.g. "sentry"
    MappingID          // which Mapping resolves payload fields
    Workflow           // registered workflow name to dispatch to
    Version            // bumped on every edit
    Status             // draft | pending_approval | armed
    Secret             // HMAC-SHA256 shared secret, encrypted at rest, never returned by GET
}
```

Bindings live in the store (SQLite), not `config.toml` -- operators author
them from the dashboard while the daemon runs, without a restart.

### Matching

`Matcher.Source` is the only predicate today: the path segment a sender
POSTs to (e.g. a webhook hitting `.../sentry` matches bindings with
`Matcher.Source == "sentry"`). The storage column is plain `TEXT`, so adding
predicates later is non-breaking -- extend `Matcher`, don't invent a second
matcher shape.

### Mapping

`mapping.Resolve(fields, payload)` walks a captured JSON payload against a
list of typed `Field`s (name, JSON path, expected type), returning resolved
values plus a `[]Failure` for anything that didn't parse or type-match. This
is schema-by-example in practice: a mapping is authored by pointing at a
real captured payload in the dashboard, not by guessing a schema in advance.

### Versioning

`Version` increments on every `UpdateBinding` call. This exists so an
editor's later change can never silently rewrite the historical provenance
of a task that already fired -- a dispatched task's binding reference is
pinned to the version that was armed when it fired, not "whatever the
binding currently says."

### State machine

```
draft ---> pending_approval ---> armed
  ^              ^                 |
  |              |                 |
  +--------------+-----------------+
        (any edit to an armed binding drops it back to pending_approval)
```

Modeled directly on Telegram's existing `dangerousAction`/`pendingApproval`
flow (`internal/channels/telegram/dangerous.go`, `approval.go`) -- not a new
approval mechanism invented for this feature. **Only `armed` evaluates
against incoming events** (`binding.Matches`, `internal/domain/binding/
binding.go`); `draft` and `pending_approval` bindings are inert. An edit to
an already-armed binding cannot bypass approval by silent mutation -- it
drops back to `pending_approval` and requires an explicit `Approve` call to
re-arm.

## The threat model

This is a public, unauthenticated-by-default intake surface whose output is
an agent with a repo, a worktree, and tool access. The design brief
(`archie-core-t2db.5`, `docs/prds/webhook-intake-security.md`) named five
non-negotiables; each is verified below against the actual shipped code,
not the design intent.

### 1. Per-source authentication

Reuses `internal/channels/webhook`'s existing HMAC-SHA256 scheme --
capture accepts unsigned events (visible in the dashboard, marked
unauthenticated), but **only authenticated events can ever match a
binding**. `binding.Matches`' doc comment states this plainly: "an
unauthenticated event can never trigger a binding, no matter how well it
would otherwise match." Enforced structurally at two points, not just
convention:

- `captured_events.authenticated` is a real column (`internal/store/
  captures.go`), set at capture time.
- `dispatchBindings` (`internal/daemon/daemon.go:479`) walks **authenticated
  captures whose source has at least one armed binding** -- an
  unauthenticated capture is never even considered for dispatch.

### 2. Human approval before a binding goes live

Covered by the state machine above. Nothing self-arms: `pending_approval ->
armed` requires an explicit `Approve` call, and any edit to an armed
binding reverts it.

### 3. Rate and volume limits per source

Capture writes are bounded independently of the approval gate, so a flood
of unauthenticated traffic cannot exhaust disk or drown the inspector:
`InsertCapture` (`internal/store/captures.go`) prunes by both **retention
window** (`WHERE received_at < cutoff`) and **max event count** in the same
insert transaction.

### 4. Blast radius bounded by the same code-level controls every task gets

A playbook-originated task is dispatched into the **same** gate, diff-cap,
sandboxed-container, worktree-isolation pipeline every task goes through
regardless of origin. Verified precisely, not assumed: `dispatchOneBinding`
calls `EnqueueBindingTask` (`internal/store/store.go`), which wraps
`EnqueueChatTask` -- the same direct-to-`tasks`-table enqueue path
chat-spawned tasks already use, then stamps `binding_id`/`binding_version`
for provenance in a second statement. This is a **different** enqueue
mechanism from forge-issue polling's `workintake.TaskEnvelope`/NATS path
(`docs/prds/event-sources-and-reactions.md`) -- binding and forge-issue
tasks arrive by different producers, but both become an ordinary
`store.Task` row that `ClaimNext` picks up identically, so both get the
same gate/worktree pipeline downstream regardless of which producer created
the row. Payload content is
ordinary untrusted prompt input; the sender's text is never treated as a
trusted instruction (Archie's environmental-enforcement principle: "the
agent should ignore instructions in the payload" is not itself a control,
the surrounding sandbox is).

### 5. Captured payloads may contain secrets from the sending system

Two independent protections, not one:

- **Redaction before persistence**: `webhookguard.RedactPayload` walks the
  decoded JSON and redacts recognised sensitive keys before the row is
  written -- the raw, unredacted payload is never durably stored.
- **Retention**: a configurable window (`InsertCapture`'s `retention`
  parameter) prunes old captures automatically; visibility of what remains
  is gated by the same dashboard authentication as every other webui
  surface, not a separate, weaker path.

### Secrets at rest

`Binding.Secret` (the HMAC shared secret a sender signs with) is encrypted
at rest via AES-GCM (`internal/store/binding_cipher.go`, `archie-core-
t2db.7`) -- a leaked SQLite file does not leak sender-side shared secrets in
plaintext. The envelope is versioned (`bindingEnvelopeVersion`) so a future
cipher or KDF change can roll forward without breaking already-encrypted
rows, and the AAD binds ciphertext to the binding-secret context so it
cannot be relocated to another field/column and still authenticate. A `nil`
cipher (no encryption key configured) leaves secrets as plaintext --
documented legacy behaviour, not silent degradation.

### At-most-once dispatch

`dispatchBindings` enqueues at most one task per `(binding, capture)` pair,
enforced by a real ledger, not best-effort application logic:
`RecordDispatch` (`internal/store/bindings.go`) does `INSERT OR IGNORE` into
`binding_dispatches`, unique on `(binding_id, capture_id)`; a duplicate
insert returns the sentinel `ErrAlreadyDispatched`, which the dispatch loop
treats as "another cycle raced us," not an error. This guarantee holds
**across daemon restarts**, not just within one process's lifetime, because
the ledger is a durable table, not in-memory state.

This ledger is also the direct precedent for the still-open EDA playbook
engine idempotency question (`archie-core-t2db.17`, `docs/prds/eda-playbook-
engine.md`'s execution-time gap 2) -- the same `(owner, event)`-keyed
`INSERT OR IGNORE` shape almost certainly generalizes to playbook-run
idempotency rather than needing a new mechanism invented from scratch.

## What this document does not cover

- The `ARCHITECTURE.md` task-lifecycle section noting that a
  playbook-originated task has different provenance from a forge-issue
  task -- open, part of `archie-core-t2db.6`.
- An end-to-end operator walkthrough (point an external app at archie,
  build a first playbook) -- open, part of `archie-core-t2db.6`.
- The playbook YAML / CEL / Module/Channel/Forge action-position work
  (`docs/prds/eda-playbook-engine.md`, `module-position.md`,
  `channel-forge-positions.md`) -- a different, newer mechanism than the
  binding model described here; the two are related but not the same
  system, and this document does not attempt to unify them.
