# Channel and Forge action positions -- investigation (resolves `t2db.19`)

**Status:** Investigation complete for Forge; Channel corrected and flagged
for a decision only Sam can make -- not a design recommendation.
**Date:** 2026-09-03
**Parent:** `docs/prds/eda-playbook-engine.md`, epic `archie-core-t2db`
**Blocked on:** `archie-core-t2db.17` (idempotency keying) for implementation
of either position -- both are side-effecting.

## Forge: ready to design once t2db.17 lands

`internal/forge.Forge` is already exactly the shape a native-Go action
position needs -- a real, typed, general-purpose interface, not something
that needs a Yaegi/Module-style schema-generation detour:

```go
type Forge interface {
    IssueForge        // AssignedIssues, IssuesWithLabel, CloseIssue, ...
    PullRequestForge  // CreatePR, PRState
    RepoForge         // AcceptInvitations, VerifyPush, LinkBranch, ...
}
```

**Typed contract shape: native-Go-by-name, not a Module-kind.** Module's
whole reason to exist is giving *Yaegi-authored, operator-supplied* code a
typed contract to implement. `Forge` is already a typed Go interface with
real, tested implementations (GitHub, Gitea) selected at daemon startup by
`forge.New(...)`. Wrapping it in a Module-kind schema/`go:generate` detour
would duplicate a contract that already exists. A Forge action position
should be: a small set of named operations (e.g. `close-issue`, `create-pr`,
`link-branch`), each with a typed `Args`/`Result` struct mirroring the
matching method's parameters/return.

**Correction (2026-09-03, caught in review before this landed as settled
design):** the dispatch target must be `internal/domain/workflow.Forger`
(`CloseIssue`/`CreatePR`/`LinkBranch` -- already exists, already exactly
these three operations), **not** `internal/forge.Forge` directly.
`organisation.md`'s target structure places `forge/` under
`internal/infrastructure/forge/`; today's `internal/forge` is a flat,
unmigrated legacy package in the same position `internal/workflow` was in
before its own migration. A new domain package depending on the flat
package directly would create a domain-to-infrastructure dependency that
breaks the moment `forge` migrates. `workflow.Forger` is the narrow,
already-domain-owned contract that exists for exactly this reason -- the
daemon's real `forge.Forge` client already satisfies it at the wiring
layer today. Depend on `Forger`, not on `internal/forge`.

**Operation scope for a first slice:** `close-issue` and `link-branch`
(single-call, cleanly idempotent by event+issue identity) are the safe
starting set. `create-pr` is explicitly a heavier case -- opening a PR is a
harder-to-reverse action with its own dedup shape beyond "did this event
fire before" (a redelivered event must not open a second PR for the same
logical change) -- defer it to a follow-up slice once `t2db.17`'s keying
scheme is proven on the simpler operations, don't design it blind now.

**Host access:** the coordinator needs the same `Forge` instance
`internal/daemon`'s poller already holds (constructed once at startup via
`forge.New`). This is a daemon-composition wiring question (pass the
existing instance into the coordinator's constructor), not a new
extensibility mechanism.

## Channel: the crew's assumption doesn't hold -- flagged, not designed

The investigation backlog that recommended scoping this alongside Forge
assumed `channels.Channel` already has an addressable send capability that
just needs a name-based lookup. **That's not true.** Verified directly:

```go
// internal/gateway/gateway.go
type Gateway interface {
    Name() string
    Start(ctx context.Context, router *Router, lifecycle Lifecycle) error
    Stop(ctx context.Context) error
}
```

`channels.Channel` embeds exactly this plus `ConfigSchema`/`ValidateConfig`
-- lifecycle and config introspection, nothing about sending a message.
Grepping `internal/channels` and `internal/gateway` for any `Send` method
returns nothing. The only generic outbound-message mechanism in the entire
codebase is `Notify` (`internal/config`'s `[notify]` block) -- a single
fixed webhook URL for gate failures/approvals/parked tasks, not a
per-channel-addressable send.

Real per-channel sending exists, but deliberately not as a reusable
capability: `telegramApprover` (`internal/channels/telegram/approval.go`)
captures a `*bot.Bot` plus a specific `chatID`/`threadID`/`recipient` at
construction, scoped to one conversation for one launch. This is the
established precedent in this codebase (already in project memory from
before this session) precisely to prevent reading a shared bot handle
across chats -- a playbook-triggered send has no chat turn to be scoped to,
so this precedent doesn't transfer as-is.

**This means "Channel action position" is not a small wrapper over an
existing capability -- it requires deciding whether to build a genuinely
new send capability that doesn't exist today, and if so, what it targets.**
Two different things could reasonably be meant by "Channel" here, and they
have different shapes:

1. **A new, playbook-scoped notification capability** -- closer in spirit
   to `Notify` (fire a message somewhere, no chat-turn context needed) but
   addressable by name/target instead of one fixed webhook. This would be
   new engine-family work (its own typed contract, registry, narrow host
   access), not an extension of `channels.Channel`.
2. **Reusing the interactive chat channels** (Telegram, email) for
   playbook-originated sends -- requires deciding what conversation context
   a playbook-triggered message has (which chat? whose approval flow?) when
   there was no incoming chat turn to anchor it, and building whatever
   capture/scoping mechanism the interactive channels use today, generalized
   to a non-chat trigger. This is materially harder and touches the
   gateway's session model.

**This is not a call to make unilaterally.** It changes what "Channel
action" even means, and the answer affects the trust/scoping model, not
just an implementation detail. Recommend: Sam decides which of the two (or
a third option not listed here) before any Channel position design work
starts. Until then, `t2db.19` should be treated as **Forge scoping only**
-- the Channel half stays genuinely open, not just unimplemented.

## Packages this touches (Forge only, once `t2db.17` lands)

- `internal/domain/eda` (or a new sibling package, e.g.
  `internal/domain/eda/forgeaction`): typed `Args`/`Result` per operation,
  a small registry keyed by operation name, dispatched against a
  `workflow.Forger`-typed value -- not `internal/forge.Forge` directly (see
  correction above).
- `internal/app/archied`: wiring the coordinator's constructor to receive
  the daemon's already-built `forge.Forge` client, passed in as the
  `workflow.Forger` it already satisfies -- no new lifecycle to manage,
  and no change to `internal/forge` itself.
