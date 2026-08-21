# Event sources and reactions -- decision

**Status:** Decided, not yet implemented
**Date:** 2026-08-22
**Beads issue:** `archie-core-7d5u.1`, blocks `7d5u.2/.3/.4/.5`

Answers the four questions in `7d5u.1`'s description. Investigated against
the actual tree, not designed from scratch: three mechanisms already in this
codebase answer most of this, and the job here is picking the right one for
each question and saying which packages change.

## 1. In-process typed families, not out-of-process dispatch

Archie has no out-of-process extension mechanism today. The plugin engine
rule (`ARCHITECTURE.md#plugin-engine-rule-strict`, mechanically checked by
`internal/plugin/architecture_test.go`) keeps every capability family
in-process behind a narrow typed registrar; NATS RPC exists for
daemon<->agentexec, a different actor with a different trust boundary (task
execution, not third-party extension code), not a precedent for generic
plugin dispatch. Neither epic's near-term payoff -- react to a labelled
issue, bind a captured webhook to a workflow -- needs extension code in
another language or process. Building out-of-process dispatch now would be
designing for a requirement neither epic states.

`docs/architecture/plugins-and-extensions.md` does **not** need amending.
The line that looked like a blanket ban on hooks (`archie-core-7d5u`'s notes
record the correction) is scoped to Workflow semantics specifically, and
nothing decided here touches Workflow's ownership of its own contracts.

## 2. Reaction delivery: `internal/eventbus`'s contract, a NEW stream

`internal/eventbus.Publisher`/`Consumer` already has everything a reaction
needs: `PublishUnique(subject, idempotencyKey, payload)` for at-least-once
without duplicate enqueue, `Ack`/`Nak` with "a handler returning an error
causes redelivery." Do not design new delivery semantics; use this contract.

**Do not reuse the existing `nats.Client` as-is.** `Config.StreamName` and
`Config.Subjects` are already composition-supplied ("the bus must not know
which subjects belong to which domain," `config.go:35-38`), but the
retention policy is not parameterized: `New` hardcodes
`Retention: jetstream.WorkQueuePolicy` (`client.go:65`), the competing-consumer
policy correct for `ARCHIE_TASKS` task distribution -- one message, claimed by
exactly one consumer -- and wrong for reactions. Two independent reactions
both wanting to see the same event under `WorkQueuePolicy` hit exactly the
trap `CLAUDE.md` already documents: overlapping filter subjects on a
work-queue stream mean the second consumer silently gets nothing. Reactions
need fan-out, not claim-once, so the client needs retention made a `Config`
field (defaulting to today's `WorkQueuePolicy` so `ARCHIE_TASKS` is
unaffected), and a second stream (`ARCHIE_REACTIONS` or similar) configured
under a fan-out-capable policy, alongside the existing task-distribution
stream -- not instead of it.

This is where `7d5u.2`'s embedded-vs-external axis attaches: whichever NATS
instance backs `ARCHIE_TASKS` also backs the reaction stream. Confirmed with
Sam 2026-08-22: NATS was never meant to be optional as a technology -- only
whether it runs as an external server or embedded in-process
(`github.com/nats-io/nats-server/v2` is already a direct, test-proven
dependency, just never wired into production startup). `7d5u.2` wires that;
this document does not re-decide it.

## 3. Reactions in these two epics are producer-only

No veto, no mutation of in-flight work. Two existing shapes cover everything
either epic actually needs, and neither is a hook that can block something
else:

- **Cheap wake, no payload.** `internal/domain/curator/wake.go`'s
  `WakeOnPrimaryInput` subscribes to the lossy `events.Bus` and nudges a
  runtime to check sooner; a dropped event only delays a check-in, never
  loses work, because the trigger decision is a deterministic state read at
  pass time, not the event payload. Model any "check something sooner"
  reaction on this, unchanged.
- **Produce new work, real payload.** `daemon.go`'s existing `pollNATS` ->
  `publishTask` path is already exactly a reaction that observes forge state
  and produces a `workintake.TaskEnvelope`. A webhook source is a second
  producer into the same path, not a new capability -- see question 4.

A reaction that can veto or mutate an *in-flight* task (block a PR from
opening, alter a running stage) is a different, harder problem with its own
ordering and failure semantics, and belongs to `archie-core-h019` (adversarial
self-review) if and when that epic needs one -- not here. Do not build a
general block-capable hook mechanism for either epic; nothing in their stated
scope requires it, and it would duplicate whatever h019 eventually needs.

## 4. No single generic intake interface; each domain keeps its own

A `Source` interface generic enough to cover a forge issue, a chat message,
and a cron tick would need an `any` payload -- the untyped-hook shape the
plugin engine rule exists to prevent, just moved into a new package instead
of the old one.

**What's genuinely shared, and belongs in a small cross-cutting package**
(imports nothing per the cross-cutting rule): the mechanics from
`archie-core-t2db.5` that apply to any inbound HTTP source regardless of what
consumes it -- HMAC verification (already a standalone function,
`internal/channels/webhook/webhook.go`'s `validateHMAC`), per-source rate
limiting, capture/audit, and the draft/pending_approval/armed gate. This
package owns none of forge, chat, or workintake's vocabulary.

**What stays domain-owned, per existing convention:**
- Forge-issue intake needs no new type. `internal/domain/workintake.TaskEnvelope`
  already is the typed contract -- `Subject()`, `IdempotencyKey()` (keyed on
  `owner/repo/number`, not on delivery source), `Encode()`. A webhook-sourced
  envelope for the same issue produces the *same* idempotency key as a
  poll-discovered one, so `PublishUnique` dedups the two paths for free. This
  is `7d5u.5`'s answer, not a new mechanism: reuse `TaskEnvelope` and
  `publishTask`, don't invent a second envelope shape.
- Chat-webhook intake stays exactly what it is:
  `internal/channels/webhook`'s `RouteConfig` routes into `gateway.Router`,
  documented as chat-scoped ("External services push messages into
  archie-core's gateway for LLM processing"). Do not generalize this package
  into a multi-domain receiver -- it already does one thing per "a package
  owns its own format end to end."
- A forge webhook receiver (`7d5u.4`) is new: it builds on the shared
  mechanics above, decodes a GitHub payload into the *same*
  `workintake.TaskEnvelope`, and calls the *same* `publishTask` the poller
  calls. It does not get its own dispatch predicate -- `7d5u.4`'s bead
  already requires reusing the poller's label/assignee predicate rather than
  reimplementing it, and this decision extends that to the envelope and
  publish path too.

## Packages this touches

- New: a cross-cutting intake-mechanics package (HMAC, rate limit, capture,
  approval state machine) -- name and exact location decided in `t2db.1`/`t2db.5`.
- `internal/infrastructure/eventbus/nats`: a second stream/consumer
  configuration for reactions, distinct retention policy from `ARCHIE_TASKS`.
- `internal/domain/workintake`: unchanged in shape; `TaskEnvelope` is reused,
  not extended.
- `internal/daemon`: gains a webhook-sourced call path into the existing
  `publishTask`, alongside the existing poll-sourced one.
- `internal/channels/webhook`: unchanged. Not the forge receiver.
