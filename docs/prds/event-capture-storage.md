# Event capture storage -- decision

**Status:** Decided, not yet implemented
**Date:** 2026-08-22
**Beads issue:** `archie-core-t2db.1`

`webhook-intake-security.md` decided points 1-5 (auth split, approval gate,
rate limits, blast radius, retention/redaction) and explicitly left the
storage mechanism open, forward-pointing at "`7d5u.2`, embedded NATS." That
pointer predates `7d5u.3`'s 2026-08-22 narrowing, which assigned capture
storage to this bead by name and told it to decide NATS-vs-SQLite itself
rather than inherit an answer. This document is that decision.

## Decision: SQLite, not NATS JetStream

Captured events are stored in archied's existing SQLite database, in a new
`captured_events` table following the same shape as `internal/store/events.go`
(`internal/store`, `*Store`, SQL migrations inline in Go). No JetStream
stream is introduced for capture.

### Why

**NATS is not unconditionally available today.** `config.NATSConfig.URL`
empty means no NATS client is constructed (`bootstrap.go`); embedded NATS
(`7d5u.2`) that would remove this gap is still open. `t2db.1`'s own
acceptance criteria require single-process behaviour to be a *deliberate*
decision, not an accident -- making capture NATS-only today would silently
regress the `local-ollama-standalone.toml` deployment profile, which this
epic's own precondition line ("capture is the precondition for everything
else") argues against. SQLite is the one store guaranteed present in every
deployment shape, because it already backs task state unconditionally.

**Capture's access pattern doesn't need what JetStream is for.** JetStream
earns its complexity for reaction delivery (`7d5u.2`) because multiple
independent consumers need fan-out, at-least-once redelivery, and Ack/Nak
semantics -- exactly the trap `CLAUDE.md` documents about `WorkQueuePolicy`
and overlapping consumers. Capture has exactly one writer (the HTTP handler)
and one reader (a human via the dashboard inspector, `t2db.2`). There is no
competing-consumer problem to solve and no redelivery semantics to get right,
so JetStream's core value proposition doesn't apply here. A bounded SQL
table is a better fit on the merits, not merely as a fallback.

**Reusing `7d5u.2`'s stream, once it exists, would still be wrong.** That
stream is provisioned for fan-out reaction delivery with its own retention
policy decision. Capture's retention need -- age- and count-bounded, prune
on write, no replay -- is a different shape. Building a second stream just
for capture, gated on an unrelated bead landing first, would trade a small
amount of duplication now for a hard dependency on `7d5u.2`'s timeline that
this bead's own acceptance criteria say not to accrue by accident.

This does not foreclose NATS later. If cross-daemon capture sharing or
durable replay becomes a real requirement, that is a new decision made
against a real need, not now against a speculative one.

## Storage shape

`captured_events` table (`internal/store/captures.go`, mirrors
`internal/store/events.go`'s pattern):

| column          | type    | notes                                          |
|-----------------|---------|-------------------------------------------------|
| id              | INTEGER | primary key, autoincrement                      |
| received_at     | TEXT    | RFC3339Nano                                     |
| source          | TEXT    | opaque path segment chosen by the sender's operator |
| remote_addr     | TEXT    | `RemoteAddr` at capture time                    |
| content_type    | TEXT    | `Content-Type` header                           |
| headers         | TEXT    | JSON, redacted                                  |
| body            | TEXT    | JSON, redacted (`webhookguard.RedactPayload`)   |
| authenticated   | INTEGER | 0/1; always 0 until a source registers a secret (future: `t2db.4`) |

Indexed on `(source, id)` for per-source listing and pruning, and `id` alone
for the global cap.

## Disk-bound mechanics

Three independent bounds, composed, per `webhook-intake-security.md` point 5
and this bead's own "capture cannot exhaust disk" criterion:

1. **Per-request size cap.** The HTTP handler reads the body through
   `http.MaxBytesReader` (256 KiB default, configurable). A single oversized
   POST is rejected (413) before redaction or storage sees it.
2. **Per-remote-address rate limit.** `webhookguard.RateLimiter`, keyed by
   the request's remote address, applied before the body is even read.
   *Not* keyed by the `source` path segment: `RateLimiter`'s own doc comment
   states its bucket map assumes a bounded, operator-registered key space,
   never evicted. `source` is exactly the opposite here -- an unregistered,
   attacker-chosen URL segment, since capturing senders with no registration
   yet is this endpoint's entire purpose. Keying on it would let one sender
   bypass the limit by rotating the segment every request (a fresh key
   always starts with a full burst) and grow the bucket map without bound.
   Remote address is the bounded identity actually available pre-auth.
   Over-budget requests get 429, observable per point 3 of the security
   decision.
3. **Retention prune-on-write.** Every insert prunes, in the same
   transaction: rows older than `retention_days` (default 7, per the
   security decision) OR beyond `max_events` total rows (default 5000, this
   bead's own addition -- age alone doesn't bound disk under a sustained
   flood inside the retention window; a count cap does). No background
   sweep goroutine; the daemon doesn't need one for this.

Together these give a deterministic worst-case: `max_events * max_body_bytes`
regardless of sender behaviour, independent of whether the age-based or
count-based bound is what actually fires first.

## HTTP surface

Mounted on the existing `internal/webui` dashboard server
(`Server.Handler`'s `top` mux), not a new listener. `POST
/webhooks/capture/{source}` bypasses `requireToken` the same way `/healthz`
already does -- capture must accept unauthenticated senders by design (that
is the entire point: you cannot HMAC-verify a source you haven't configured
a secret for yet). Reachability is therefore whatever already exposes the
dashboard in a given deployment; no new port/host config surface is added,
and no new claim about public reachability is made beyond what already
exists for the dashboard.

`GET` listing/inspection endpoints (`t2db.2`) stay behind `requireToken` like
every other `/api/*` route -- captured payloads are visible only to an
authenticated operator, per the security decision's point 5 visibility rule.

## Config

New `[capture]` section in `config.example.toml` / `internal/config`:

```toml
[capture]
retention_days = 7
max_events     = 5000
max_body_bytes = 262144
rate_per_second = 1.0
rate_burst      = 5
```

All fields have defaults; an empty `[capture]` section (or its absence) is
valid and produces the defaults above -- capture is on by default, since an
unbound POST being captured rather than rejected is this bead's acceptance
criterion, not an opt-in.

## What this does not decide

Binding a captured event to a workflow, the draft/pending_approval/armed
state machine, and per-source HMAC secret registration are `t2db.3`/`t2db.4`.
The dashboard inspector UI is `t2db.2`. This bead ships capture and storage
only.
