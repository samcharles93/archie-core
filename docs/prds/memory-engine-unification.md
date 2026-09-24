# Memory engine unification: four scopes, CRUD, and one write path -- decision

**Status:** Approved
**Date:** 2026-09-16
**Findings:** `memory-4` and its dependants in the adversarial sweep
(`.local/issue-tracker`); the real defect is the split described below, for
which `memory-4` is a symptom
**Supersedes:** the draft `memory-turn-context.md` (its synchronous-read
argument survives and is reused; its `Manager` method and consumer-owned port do
not)
**Settles:** four open items in `docs/architecture/migration-decisions.md#5`
(listed at the end)

## Problem

There are **two live memory stores for one assistant**, split by which component
writes them, and the chat turn reads neither.

| Family                                                      | Written by                                                                    | Root                      | Format                  |
| ----------------------------------------------------------- | ----------------------------------------------------------------------------- | ------------------------- | ----------------------- |
| `internal/memory` (`Manager`, `builtin.Provider`)           | the chat agent's `memory_edit` tool via `memorytoolprovider` (`bootstrap.go`) | `<workDir>/memory`        | `MEMORY.md` / `USER.md` |
| `internal/domain/memory` + `internal/infrastructure/memory` | the session curator (`sessioncurator.go`)                                     | `<workDir>/memory-engine` | identity-keyed blocks   |

They share no code, no format and no scope vocabulary. Neither reaches a prompt:
`Manager.SystemPromptBlock` (`internal/memory/manager.go`) has no production
caller anywhere, so the agent can write memory through a tool and never read it
back.

Three further defects fall out of the same split:

1. **The engine's contract is not CRUD.** `Store` is `Write`/`Query`/`List`/
   `Forget` — create, read, delete, and **no Update**. The retired file provider
   _does_ have `handleAdd`/`handleReplace`/`handleRemove`, so the family being
   deleted is operationally richer than its replacement.
2. **There is no scope model.** The contract carries one opaque `Identity
string`, and the curator fills it with a **session id** (`sessioncurator.go`)
   — which is none of the four scopes `docs/architecture/agent-system.md`
   requires, and stops meaning anything when the session ends.
3. **The chat turn has no user identity on half the channels.** Telegram and
   email populate `SenderID` with a person; the dashboard populates it with
   nothing, and a webhook populates it with nothing either — a route path names
   a source, not a person, so `webhook.go` leaves `SenderID` empty and carries
   the route in `Inbound.BudgetKey` for rate limiting. `sender_id` is persisted
   (`session_store_postgres.go`), so whatever a channel puts there reaches the
   consumers that read it.

`agent-system.md` says the write-scope selection and cross-scope sharing
rules "require a focused memory design decision". This is that decision.

## Decision

### 1. One engine, addressed by four typed scopes

`internal/domain/memory` is the only memory engine. `Observation.Identity`,
`Query.Identity`, `Record.Identity` and `List(identity)` are deleted, not
extended: a field whose meaning depends on the caller cannot carry a scope.

```go
type ScopeKind string

const (
    ScopeGlobal    ScopeKind = "global"     // shared across the application
    ScopeAgent     ScopeKind = "agent"      // private to one Agent
    ScopeUser      ScopeKind = "user"       // one user, across their Agents
    ScopeAgentUser ScopeKind = "agent-user" // one Agent's relationship with one user
)

type Scope struct {
    Kind  ScopeKind
    Agent AgentID    // required for ScopeAgent, ScopeAgentUser
    User  IdentityID // required for ScopeUser, ScopeAgentUser
}

func (s Scope) Validate() error   // rejects a component present where it is not required
func (s Scope) Key() string       // canonical, collision-free storage key
```

`AgentID` and `IdentityID` are opaque. This package never resolves or
canonicalises them.

A turn's resolved identity is one value:

```go
type Subject struct {
    AgentID AgentID
    UserID  IdentityID // "" when the channel carries no per-person identity
}

func (s Subject) Scopes() []Scope         // readable, most specific first
func (s Subject) WritableScopes() []Scope // never includes ScopeGlobal
```

**Access control is naming a scope, and nothing else.** The read path names
exactly the scopes of the resolved `Subject`; the engine never unions beyond what
it is handed and applies no policy of its own. An engine that enforced policy
would need to know about channels, sessions and bindings, and every backend would
reimplement it. "Agent-wide is private to its owning Agent by default"
(`agent-system.md`) falls out mechanically, because `ScopeAgent{a}` and
`ScopeAgent{b}` are different storage keys.

`ScopeGlobal` is never model-writable. Writing it is an operator action; nothing
produces one yet, but the contract models it because the read set includes it.

### 2. CRUD, with revisions retained

```go
type Store interface {
    Create(ctx context.Context, in NewRecord) (Record, error)
    Get(ctx context.Context, scope Scope, id RecordID) (Record, error)   // ErrNotFound
    Query(ctx context.Context, q Query) ([]Record, error)                // heads only
    List(ctx context.Context, scope Scope) ([]Record, error)             // heads only
    Update(ctx context.Context, in RecordUpdate) (Record, error)         // ErrNotFound, ErrStaleRevision
    Forget(ctx context.Context, scope Scope, id RecordID) error          // idempotent
    Revisions(ctx context.Context, scope Scope, id RecordID) ([]Revision, error)
}
```

`Get` and `Forget` take a scope. The current `Forget` scans every identity's
store to find a marker (`builtin_engine.go`) purely because the contract
gave it nothing better.

`Record` carries what `agent-system.md` requires be retained: `Scope`,
`Kind`, `Content`, `Revision`, `Author`, `OriginUser`, `Source`, `CreatedAt`,
`UpdatedAt`. `Metadata` is dropped — nothing sets it and the parser never
restores it, so it is a field that silently does not round-trip.

`Update` supersedes rather than overwrites. The superseded state is appended to
the scope's `HISTORY.md` **before** the live block is replaced, so a crash
between the two leaves an extra history entry and an unchanged record —
recoverable — never a record whose previous state was lost. `Update` carries no
`Scope`: a record may never move between scopes, and the API makes that
unrepresentable rather than validating it. `Expected` optionally names the
revision being replaced and returns `ErrStaleRevision` on mismatch, which matters
because two processes (`archied` and `archie-gateway`) can hold the same scope's
file.

`Forget` records a deleted revision before removing the live block, so a record's
provenance outlives its content.

### 3. The caller resolves identity; the engine never does

Resolution happens in the gateway turn path, where the native sender id and the
session are both in hand, and it is supplied **per channel by the composition**:

```go
// TurnRunnerConfig
// UserIdentity resolves the initiating user's identity for one inbound message.
// false means the channel carries no per-person identity, and the turn gets
// global and agent scopes only. Nil means the same.
UserIdentity func(msg messaging.Message) (IdentityID, bool)
```

This must be injectable rather than a blanket `channel + ":" + SenderID`
concatenation, because a channel's sender id is not necessarily a person: a
webhook's route path names a source, and treating it as an identity would give
a URL one -- which is why `webhook.go` leaves `SenderID` empty and carries the
route in `Inbound.BudgetKey` instead. Telegram and email supply their sender
id; the dashboard and webhook resolve nothing.

**Fail closed.** When `UserID` is empty the turn proceeds with global and agent
scopes only, and a write targeting `user` or `agent-user` fails with a clear
error. `agent-system.md` forbids showing one user's memory to another, so
"we do not know who this is" must mean "show none of it", never "fall back to a
wider scope".

### 4. The chat read path

Synchronous and total. It runs inside `prepareTurn` (`turn.go`) before the
prompt is built, never returns an error to the caller, and degrades to an empty
block on any engine failure or provider panic. A fire-and-forget hook would race
the prompt build in the same turn, which is why this is not a lifecycle hook.

The read is a scope-only `Query` over the subject's scopes — **not** ranked
search, which remains open in `migration-decisions.md#5`. A ranked read on the
hot path with no ranking implementation would be a lie.

`SystemPromptConfig` gains a `Memory` field, rendered into
`templates/archie.md.tpl` between the `<tools>` and `<env>` blocks:

```
<memory purpose="durable_context" trust="data">
```

`trust="data"` and inclusion in the precedence clause follow the `<tools>`/`<env>`
precedent exactly: memory content is model-written from untrusted input and must
be framed as data, never instructions. The rendered block carries each record's
short id, because the write path addresses records by id — one source of truth
for both directions.

Both a per-scope record limit and a byte cap on the rendered block are named
constants. The cap is about size, not latency; a local read needs no timeout.

**Prerequisite:** `setupGateways` runs before `setupMemoryAll`
(`main.go` vs `:428`), and `startGatewayRuntime` before `setupMemory`
(`gateway.go` vs `:115`), so the engine is nil at every turn-runner
construction site. The ordering is the root-cause fix and nothing in either
memory step reads what those later steps produce.

### 5. The chat write path

The memory tool is built **per turn**, not registered once at boot, because it
must write into a scope derived from the turn's resolved subject:

| action   | engine call      |
| -------- | ---------------- |
| `create` | `Create`         |
| `update` | `Update` (by id) |
| `delete` | `Forget` (by id) |
| `list`   | `List`           |

**The model chooses the scope kind; it never chooses the ids.** Agent and user
ids come from the turn's resolution and there is no schema field to put anything
else in — even a model that emits another user's id has nowhere to write it. That
is the root-cause form of `agent-system.md`, matching this system's other
non-waivable boundaries (the model never runs git; gates cannot be waived by the
agent). `global` is not offered at all.

The tool takes a record **id** where the retired provider took a content
substring (`handleReplace`/`handleRemove`). The substring addressing existed
because a markdown file was hand-edited and the model had no stable handle on a
block; with ids rendered into the prompt it disappears, and one addressing scheme
replaces two.

### 6. The curator's write

`sessioncurator.reviewOne` stops writing `Identity: sessionID` and writes
`ScopeAgentUser{AgentID: <session's agent>, UserID: <participant>}`. Relationship
scope is the only defensible home: agent-wide is explicitly unsafe for user facts
and would leak user A's extracted facts to user B through the same agent;
user-wide would pool facts across agents; and a session id cannot survive the
session ending, which is the whole requirement.

This needs data the tree does not persist: the `messages` table
gains `sender_id`, and `curator.SessionSummary` / `ConversationMessage` gain the
agent and participant. The participant is derived from the session's own
user-role messages: exactly one distinct non-empty sender writes; zero (a
dashboard or webhook session) or several (a group chat) writes nothing and
records why. Ambiguity fails closed rather than guessing.

The payoff is the point of the exercise: chat-written and curator-derived memory
now occupy the same scopes, so a future turn's prompt contains both, and a
curator's bad note becomes id-addressable and therefore correctable by the agent.

### 7. The content scanner moves to the engine

`internal/memory/scanner.go` and its installation at `bootstrap.go` are what
make `configuration.md`'s "Memory safety scanner" row describe a control that
actually runs. Deleting the package would silently remove prompt-injection
scanning on memory writes. The scanner moves into the engine and is applied in
`Create` and `Update`, which is the single choke point every producer crosses —
so the curator's model-extracted content is covered too, not just the chat tool's.

### 8. `internal/memory` is deleted

Not shimmed, not partially, not behind a flag: `manager.go`, `provider.go`,
`lifecycle.go`, `sync.go`, `scanner.go` (moved), `schema.go`, `backup.go`,
`config.go`, `builtin/provider.go`, and their tests. Its markdown store
(`builtin/store.go`) is the one survivor, relocated to
`internal/infrastructure/memory/builtin/` — `organisation.md`'s "a package owns
its on-disk format end-to-end" makes infrastructure its correct home, and the
engine already parses that format.

Also deleted: `internal/tools/provider/memory`'s manager wrapping (rewired to the
engine), `config.Memory.{Provider,ProviderConfig}` and the
`validateMemory` rejection that exists only to complain about `Provider`
(`validate.go`), and `webui.MemoryStatus`/`MemoryProviderHandle` — never
implemented in production, so `/api/memory` reports empty forever unless it is
repointed at the engine registry.

Existing data at `<workDir>/memory` is left on disk, unreferenced. No migration.
`<workDir>/memory-engine` is likewise orphaned: its directories were keyed by
`sha256(sessionID)` and no scope key can hash to one.

## What we deliberately do NOT do

- **No cross-scope sharing or grants in this wave.** Promoting a record between
  scopes is a future explicit command, and `Update` cannot change scope, so it
  cannot happen by accident.
- **No canonical user identity.** `SenderID` is channel-native, so one person on
  Telegram and email is two identities. Unifying needs an Identity aggregate that
  does not exist (`internal/domain` has no identity package and `IdentityID`
  appears nowhere in Go). The contract does not change when it lands.
- **No ranked retrieval.** `Query.Text` stays, unused by the prompt path.
- **No data migration**, per the decision above.
- **No prompt-memory timeout mechanism.** The read is bounded by construction.
- **No `Metadata` field.** It is write-only; a caller needing it later
  needs one field, not a speculative contract.

## Acceptance criteria

1. A record created through the memory tool in turn 1 appears in turn 2's prompt,
   asserted on content the test planted, with its id.
2. The same record is **absent** from a different user's prompt on the same
   channel and agent. Isolation asserted end to end, not at the engine boundary.
3. An agent-scope record is visible to both of that agent's users; a global
   record is visible to a second agent; an agent-user record is not.
4. A curator pass over a session writes a record a later turn reads, and which
   survives that session being deleted.
5. `create` → prompt shows it; `update` → prompt shows new content and
   `Revisions` returns the prior content with author and time; `delete` → gone
   from prompt and `List`. Round-trip is asserted as `parse(render(r)) == r`,
   never against a fixture string.
6. A panicking engine and a failing engine both complete the turn with no memory
   block.
7. A channel with no resolvable user renders only global and agent scopes, and
   the tool refuses `user`/`agent-user` with a reason. A webhook route path never
   becomes a user identity.
8. A write that would exceed the file cap fails loudly rather than silently
   dropping content.
9. `internal/memory` is gone, `task check` is green, and no document describes a
   manager that no longer exists.

Every criterion asserts behaviour or planted content. None asserts on the text of
a hand-written file, and none pins that a method was called. In particular none
asserts the composition calls setup in a given order: if the ordering were
swapped back, criteria 1 and 4 would fail, and that is the assertion.

## Sequencing

Each step is independently verifiable and lands green on its own.

1. **Persist the participant.** `sender_id` column on `messages`,
   insert/select/migration, `curator.SessionSummary.AgentID` and
   `ConversationMessage.SenderID`. Touches no memory contract and is shippable
   alone — nothing consumes it yet.
2. **Scope, CRUD and revisions.** The `internal/domain/memory` contract, the
   builtin engine with `HISTORY.md`, the store relocation, the scanner move, and
   the curator rewritten onto agent-user scope (it is the engine's only live
   consumer, so it belongs in this slice). _Depends on 1 for the curator half._
3. **Chat read path.** The `Memory` seam, the template block, subject resolution,
   and the composition-ordering fix. _Depends on 2._
4. **Chat write path.** The tool adapter onto CRUD and the turn's subject on the
   context. _Depends on 3, because a write is only observable through the read._
5. **Delete `internal/memory` and the dead config/dashboard surface**, and
   rewrite the "Memory engine family" section of
   `docs/architecture/plugins-and-extensions.md` to describe the
   engine family rather than a manager it deleted. _Depends on 3 and 4._

Steps 3 and 4 are separable from each other; step 1 is separable from everything.

## Settles these open items in `migration-decisions.md` §5

| §5 open item                                   | Settled by                                                                                                                                                |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| authoritative memory record and revision model | §1, §2 — the live marker becomes JSON, since the ratified `<!--mem:<ulid>-->` cannot carry provenance; superseded states live in a per-scope `HISTORY.md` |
| provider and infrastructure boundaries         | §2 — contract in `internal/domain/memory`, builtin format in `internal/infrastructure/memory/builtin`                                                     |
| retrieval and access enforcement               | §1 — caller-named scope sets, no engine-side policy                                                                                                       |
| provenance representation                      | §2                                                                                                                                                        |

## Not determined

- **Group chats produce no derived memory**, because the curator fails closed on
  mixed-sender sessions. Whether extraction should instead become
  per-participant is a product call.
- **Two processes, one scope file.** After step 3 both `archied` and
  `archie-gateway` hold an engine over the same root, and the engine caches a
  store per scope and rewrites the whole file, so concurrent writers on one scope
  can lose a block. `Update.Expected` makes that detectable for one record; it
  does not make the file safe. A reload-before-mutate or advisory lock is the fix
  and is not designed here.
- **Unbounded history.** `HISTORY.md` grows without limit; no compaction policy.
- **Size bounds.** `defaultMaxFileBytes` is now per scope rather than per
  identity; whether that is right is unexamined.
- **Whether the dashboard should carry a user identity.** It has one bearer
  token, not users. Until it has users, web turns are correctly limited to global
  and agent scope.
