# Persistent session-memory curator (reference implementation) -- decision

**Status:** Approved for archie-core-1786637499114 implementation
**Date:** 2026-09-02
**Beads issue:** `archie-core-1786637499114`
**Parent epic:** `archie-core-1786637500725` (curator engine)
**Depends on:** `archie-core-1786637499636` (bind curators to memory
engines, shipped) and the `domain/memory` engine family (shipped)

## Decision

Each pass: find sessions with activity since the last pass, ask the
model to extract a short list of durable observations from each one's
recent messages, and write each through the curator's bound memory
engine. Extraction is **scoped to one session** -- `Observation.Identity`
is the session ID, not a user or cross-session identity. No
consolidation across sessions, no forgetting, no rewriting a prior
extraction: every pass only adds.

This is the second contract consumer the epic asks for, and the one that
exercises the curator/memory-engine binding
(`archie-core-1786637499636`) for real, unlike the skill curator, which
declares no memory engine at all.

## Why session-scoped, not user- or agent-wide

`docs/architecture/migration-decisions.md#5` lists the four required
memory scopes (global, agent-wide, user-wide, agent-user relationship)
as **still open** -- there is no decided mapping yet from a chat session
to a durable identity that would outlive that session (a Telegram chat
ID is not necessarily "the same person" as a web session; per-platform
identity resolution does not exist as a memory concern today). Choosing
session-as-identity sidesteps a decision this issue has no business
making unilaterally: it is the narrowest scope that is unambiguous
(`gateway.SessionContext.SessionID` already exists, is already unique,
and already has no cross-platform identity claim attached to it), and
it is forward-compatible -- widening to a resolved user identity later
is a change to what `Identity` is populated with, not to the
extraction or write path.

## Why a new `curator.ConversationSource`, not `gateway.SessionStore` directly

`internal/domain/curator` is a domain package; `internal/gateway` is not
one of `internal/domain/*`'s dependencies by convention, and
`gateway.SessionStore`'s full CRUD surface (branch, fork, replace,
search) is far more than a curator needs or should be able to reach --
the same reasoning `ToolBuilder`/`SkillStore`/`MemoryEngineSource`
already established for every other host service this package depends
on. `curator.ConversationSource` is a two-method read-only interface;
`internal/app/archied` adapts the real `gateway.SessionStore` to it, the
same shape `MemoryEngineSource` already uses for
`domain/memory.Registry`.

```go
// ConversationSource is read-only conversation history, narrowed to
// what a curator needs to review recent activity -- never the full
// session CRUD/branch/search surface a chat gateway exposes.
type ConversationSource interface {
    // RecentSessions returns every session touched at or after since,
    // newest first.
    RecentSessions(ctx context.Context, since time.Time) ([]SessionSummary, error)
    // Messages returns the most recent n messages of one session, in
    // chronological order.
    Messages(ctx context.Context, sessionID string, n int) ([]ConversationMessage, error)
}

type SessionSummary struct {
    ID         string
    LastActive time.Time
}

type ConversationMessage struct {
    Role    string // "user" or "assistant"
    Content string
    At      time.Time
}
```

`Manifest` gains `Conversations bool`, gated exactly like `Skills`:
`Registry.validateDeclared` requires a `ConversationSource` on the host
when declared, `Registry.filter` clears it from a curator's view when
not. Role is derived the same way `gateway.compressTurnHistory` already
does (`message.From == botUser` -> `"assistant"`, else `"user"`) --
copied logic, not shared code, since the source type
(`gateway.Message`) is exactly the thing this package must not import.

## Extraction shape

`Pass`:

1. `Conversations.RecentSessions(ctx, effectiveSince(in.Since))` -- see
   below for the zero-`Since` default.
2. For each session, `Conversations.Messages(ctx, id, 40)` -- the last
   40 messages, not the full history. A session accumulates far more
   history than a skill file accumulates content; unlike the skill
   curator's "re-read everything, it's cheap," a full-history read here
   is not cheap and is not needed -- durable facts worth keeping surface
   in recent exchanges, and a 40-message tail is deliberately generous
   against the model's own context rather than tuned tightly, since
   getting this number exactly right is not this issue's job.
3. One model call per session: the message tail, plus an instruction to
   return a JSON array of short strings, each one fact/preference/
   correction worth keeping, or `[]` if nothing is. Capped at 5 items
   per session per pass -- a session that produced more than 5 durable
   facts in one pass is the extraction prompt's problem to get more
   precise about, not a limit this curator should quietly lift.
4. A response that isn't valid JSON, or that fails to parse, is treated
   as "nothing extracted" for that session, not a pass failure -- the
   next pass tries again, and this is a curator, not a build gate.
5. Each extracted string becomes one `Write` through
   `Registrar.MemoryEngines.Get("builtin")`
   (`Observation{Identity: sessionID, Kind: "note", Content: text}`).
   One `Action` per session summarizes the count and lists the session
   ID; a session with nothing extracted gets no `Action`, matching the
   skill curator's "a pass with nothing to report is not an error, and
   not every subject needs an entry every time."

`effectiveSince`: `PassInput.Since` zero (first pass, or the runtime
declining to name a horizon) resolves to 24 hours before now, not "the
beginning of time." Extracting from a session's entire history on the
very first pass is exactly the kind of unbounded-cost-on-cold-start
mistake `Check` exists to keep cheap, and 24h is long enough to catch
what a daily check-in cadence would have caught anyway.

`Check`: `len(RecentSessions(ctx, effectiveSince(zero-Since))) > 0` --
cheap (no model call), and answers "is there anything a pass would find"
honestly rather than always returning true.

## What we deliberately do NOT do

- **No cross-session or cross-user consolidation.** `Identity` is the
  session ID; two sessions from the same human get two disjoint memory
  scopes until the open identity-resolution decision in
  `migration-decisions.md#5` is made.
- **No re-extraction guard, no deduplication.** A fact mentioned in two
  overlapping passes may be written twice. `domain/memory.Query`
  ranking/relevance is a read-side concern for whatever later consumes
  these observations, not this curator's job to solve by inventing a
  dedup index.
- **No `Forget`.** Nothing here decides a memory is stale; that is a
  different, harder judgment call than "is this worth keeping," and nothing
  currently reads these observations back into a chat turn to notice if
  one turned out wrong.
- **No tool declarations.** `Manifest.Tools` is empty; the model call is
  a plain completion (`ChatRequest.Tools` left nil), not agentic
  tool-calling -- the extraction task does not need tools, and declaring
  none is what keeps `Registrar.Tools` correctly absent from this
  curator's view.
- **No feeding extracted observations back into the live system
  prompt.** `internal/memory.Manager`'s existing frozen-snapshot
  mechanism is untouched, same disclaimer as the memory-engine-contract
  and builtin-engine issues before this one: the new `domain/memory`
  path and the legacy live path run in parallel by design. Reading these
  observations back into a chat turn is later, separate work.

## Acceptance criteria

Restated from the issue against this shape:

1. Registers and runs through `curator.Registry` with no special-casing
   -- the existing `Register`/`Runtime` path, no new registry method.
2. Writes through the memory engine contract, never a concrete store:
   every write goes through `Registrar.MemoryEngines.Get(name).Write`,
   resolved by name at call time exactly like the skill curator resolves
   nothing store-specific either.
3. Extraction runs off the hot path: the model call happens inside
   `Pass`, which the curator runtime already runs off a chat turn's own
   goroutine (existing runtime guarantee, not something this issue
   builds).
4. What it wrote and why is inspectable: one `Action` per session with a
   count and the session ID, through the existing curator activity
   tracker -- no new observability surface.
5. Honours the trigger-accounting invariant: `Manifest.OnInput = true`
   wakes it on `events.KindTurnCompleted` via the existing
   `WakeOnPrimaryInput` wiring, which already excludes every
   curator-derived event kind from the forwarded set -- this curator's
   own writes cannot reschedule it, with no new code needed for that
   guarantee.

## Files this change adds

- `internal/domain/curator/registrar.go` -- `ConversationSource`,
  `SessionSummary`, `ConversationMessage`, new `Conversations` field
- `internal/domain/curator/registry.go` -- `Conversations` gated like
  `Skills` in `validateDeclared`/`filter`
- `internal/domain/curator/registry_test.go` -- table cases + fakes for
  the new field, mirroring the existing `Skills`/`MemoryEngines` cases
- `internal/infrastructure/sessioncurator/adapter.go` -- wraps
  `gateway.SessionStore` as `curator.ConversationSource`
- `internal/infrastructure/sessioncurator/adapter_test.go`
- `internal/infrastructure/sessioncurator/sessioncurator.go` -- the
  `CuratorEngine`
- `internal/infrastructure/sessioncurator/sessioncurator_test.go`
- `internal/app/archied/bootstrap.go` -- register it in `setupCurators`,
  wired to `b.chatSessionStore` and `b.cfg.BotUser`
