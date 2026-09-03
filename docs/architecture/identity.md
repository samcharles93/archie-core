# Identity

**Status:** Approved foundation; lifecycle details are deferred  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Purpose

Identity answers which persistent actor owns or performed an action. It supplies
stable cross-domain attribution and lifecycle semantics.

Identity kinds include:

- the Archie system identity, such as `System` or `archied`;
- agent or bot identities;
- service-account identities;
- future user identities.

A user is another kind of identity rather than a separate foundation concept.

## Ownership

`internal/domain/identity` owns:

- immutable canonical `IdentityID` values;
- identity kind;
- lifecycle state;
- ownership and attribution semantics;
- identity-related commands and events.

Domains may record an identity reference on their own state and events when
ownership or attribution is required. They MUST NOT receive a complete
per-identity application configuration bundle.

Persona and communication style are separate from identity.

Every Agent has its own immutable acting `IdentityID`. Agent actions use that
identity for ownership and attribution in the same way an organisation records
which user accessed a resource, changed a permission, or performed another
auditable action.

Agent and Identity remain separate aggregates. Identity supplies the stable
actor reference; Agent owns assistant behaviour, memory, specialisation,
conversations, and capability context.

Agent identities MUST NOT be shared between Agents. Automated WorkflowExecutions
MAY eventually act through a dedicated service-account identity when a specific
unattended automation requires it. Such use is an explicit execution decision
and audit principal, not the default for Agents or a substitute for recording
the initiating user when one exists.

A Workflow is owned by an IdentityID and executes as its owner by default.
Future delegated execution MAY explicitly assign a different `RunAsIdentityID`,
including a user or service-account identity. Ownership, effective execution
identity, initiating identity, and delegation provenance remain distinct audit
facts.

## Canonical identity

Every identity has an immutable `IdentityID`. It is the only value used for
cross-domain ownership, attribution, routing, persistence, commands, and events.

Identity kind is explicit:

- system;
- bot;
- service account;
- user.

Display names are mutable and MUST NOT be used as identifiers. Forge usernames,
channel bot names, Git authors, provider account names, and other external
identifiers are bindings associated with an identity rather than identity
itself.

`System` is represented by a real identity and `IdentityID`. The empty string
MUST NOT retain special meaning as a legacy system identity.

Configuration refers to identities by `IdentityID` and supplies external
bindings and capability settings separately. Renaming an identity or changing an
external account MUST NOT change ownership or historical attribution.

Secret-bearing channel bindings remain configuration concerns rather than
identity data. In particular, `[chat.telegram].token` is a
`secret.SecretRef` resolved by the application through the configured secret
registry; the older `token_env` field remains a compatibility fallback. Neither
form changes the identity that owns or performs a chat action.

## Persistence

Identity records are durable domain state. The Identity domain's repository is
the source of truth; infrastructure implements its persistence contract.

Configuration MAY bootstrap a missing identity through an explicit, audited
creation operation and MAY propose runtime capability bindings for an existing
`IdentityID`. Configuration is not the identity registry.

Omitting an identity from configuration stops assembling the omitted runtime
capabilities. It MUST NOT delete the identity record, erase history, or make
historical references unresolvable.

Disabling or retiring an identity requires an explicit Identity-domain command.
Retired identities remain resolvable for historical attribution. A failed
external binding affects capability availability without destroying or silently
replacing the identity.

## Lifecycle

Identity lifecycle is deliberately small:

- `active`: may participate in runtime composition;
- `suspended`: deliberately prevented from initiating new work while ownership
  and history remain;
- `retired`: terminal, unable to initiate work, and permanently retained for
  attribution.

Allowed transitions are:

```text
create -> active
active -> suspended
suspended -> active
active -> retired
suspended -> retired
```

`retired` has no outgoing transition.

Capability health is separate from identity lifecycle. Forge, model, channel,
workspace, and other bindings report health independently. An active identity
may be partially degraded without becoming suspended or retired. Runtime
supervision isolates and restores affected bindings through the safe-change and
recovery protocol.

## System identity

Archie has exactly one built-in system identity. It is created during initial
store bootstrap, is strictly always active, and cannot be suspended or retired.
Its `IdentityID` and kind are immutable.

Startup, migration, supervision, remediation, and rollback actions performed by
the application use the system identity. The system identity MUST NOT be
delegated to individual identities. Every user, bot, service account, or other
actor mutation records the identity that actually performed it.

System capabilities may independently become unhealthy or unavailable without
changing the system identity.

## Target placement and contracts

The Identity domain lives at `internal/domain/identity`.

It owns:

- `IdentityID`, identity kind, display name, lifecycle, version, and audit
  timestamps;
- create, rename, suspend, reactivate, and retire commands;
- corresponding lifecycle events;
- the repository contract required for durable identity records;
- lookup of current and historical identities.

External account bindings and capability settings remain with the domains that
use them and reference `IdentityID`.

## Migration constraints

Migration from the current string model MUST:

- bootstrap the permanent system identity;
- assign stable IDs to configured identities without losing their names;
- replace empty-string legacy ownership with the system identity;
- migrate tasks and sessions to canonical identity references;
- include identity where task uniqueness and transport deduplication require
  independent ownership;
- route container RPC through the task owner's capability bindings;
- retain historical resolution after rename, suspension, or retirement;
- remove identity-string equality from authentication or access semantics.

This completes the foundation required to restructure the current Identity
behaviour. Exact storage schemas and Go method signatures belong to
implementation design, not this architecture review.

## Lifecycle

Identity lifecycle is deliberately small:

- `active`: may participate in runtime composition;
- `suspended`: deliberately prevented from initiating new work while ownership
  and history remain;
- `retired`: terminal, unable to initiate work, and permanently retained for
  attribution.

Allowed transitions are:

```text
create -> active
active -> suspended
suspended -> active
active -> retired
suspended -> retired
```

`retired` has no outgoing transition.

Capability health is separate from identity lifecycle. Forge, model, channel,
workspace, and other bindings report health independently. An active identity
may be partially degraded without becoming suspended or retired. Runtime
supervision isolates and restores affected bindings through the safe-change and
recovery protocol.

## Explicit non-responsibilities

Identity is not authentication and is not access control.

The current architecture does not require authentication or authorization. Those
capabilities MAY be introduced later if deployment or product needs justify
them. They MUST remain separate from the identity model.

An identity reference therefore proves attribution within Archie's trusted
system model; it does not by itself prove credentials or permission.

## Per-identity configuration

Values selected for an identity remain external configuration values. At
composition time they are split into the runtime settings owned by the affected
domains and correlated by identity reference.

The existing `IdentityConfig` aggregate MUST be dissolved. Forge accounts,
repositories, models, providers, workspace behaviour, polling, budgets,
notifications, and transport namespaces retain their respective owners.

## Audit

Configuration changes and other state changes are actions attributed to an
identity. System-initiated changes use the system identity. Their audit records
MUST preserve the acting identity, the target, and the resulting change.

The command ownership and persistence mechanism for configuration changes is a
separate configuration-management decision.

## Current implementation

Identity is not currently implemented as a domain. It is a collection of string
fields and process-local associations:

- `config.IdentityConfig.Name` is the daemon routing key.
- `config.IdentityConfig.BotUser` is the external forge and Git author name.
- `daemon.IdentityRunner` bundles the configured name with a forge client,
  worktree manager, repository list, configuration subset, logger, and polling
  goroutine.
- `store.Task.Identity` persists the configured name on tasks.
- NATS task messages copy the same configured name.
- gateway routing accepts an identity name and uses it to select a repository
  profile.
- gateway sessions persist `BotUser`, not the configured identity name.

There is no identity registry, identity record, identity lifecycle state,
identity persistence, or identity command/event model. Removing an identity from
configuration makes existing task ownership unresolvable.

An empty identity string selects the legacy single-identity path. This gives the
empty string behavioural meaning rather than representing a real system
identity.

## Current enforcement and isolation

The current implementation uses identity equality as an authorization check for
chat task approval and cancellation. This conflicts with the approved rule that
identity is attribution rather than authentication or access control and must be
reassigned to an appropriate task-ownership or future access policy.

Focused tests verify that:

- a task resolves the forge client and repository list associated with its
  configured identity name;
- cross-identity repository profiles do not leak;
- chat task creation propagates the selected identity;
- task persistence retains the identity string;
- chat task control rejects a different identity string.

## Current hazards

- SQLite task uniqueness is `(owner, repo, issue_number)` and excludes identity.
  Two identities discovering the same forge issue cannot own distinct task
  records.
- NATS deduplication uses `owner/repo/issue` and excludes identity, so discovery
  by a second identity can be discarded as a duplicate.
- Container-mode forge and worktree RPC servers are wired to the root clients,
  not the task owner's clients. The source documents this as an unresolved
  identity leak.
- The shared task queue claims the oldest task without identity partitioning and
  relies on later string lookup to recover the correct runner.
- Sessions use forge bot usernames where tasks use configured identity names, so
  there is no canonical cross-domain identity reference.
- Identity lifecycle is process lifetime only; there is no disabled, degraded,
  retired, or unavailable state and no audit history.

The focused identity tests pass. Broader daemon and NATS suites could not open
embedded listener ports in the current sandbox; their failures occurred before
identity behaviour executed.

## Three-layer identity model

Agreed 2026-07-30; the frame for SOUL
([#68](https://github.com/samcharles93/archie-core/issues/68)) and the curator
([#435](https://github.com/samcharles93/archie-core/issues/435)).
Recovered 2026-08-09 from the pre-migration issue tracker.

**Layer 1 — invariants.** Instruction precedence, core rules, and the `<tools>`
and `<env>` blocks live in `internal/gateway/templates/archie.md.tpl` and are
unreachable at runtime by anyone.

**Layer 2 — SOUL.** User-authored identity: agent name, register, warmth.
Editable without a rebuild.

**Enforcing the Layer 1 / Layer 2 boundary.** SOUL is rendered as the
`Persona` field of `SystemPromptConfig` (`internal/gateway/prompt.go`) into a
`<soul purpose="identity_and_style" trust="data">` block, XML-escaped through
the same `xml` template helper used for tool and env metadata, and placed
*before* `<instruction_precedence>` in `archie.md.tpl`. Escaping means SOUL
text can never forge a live `<core_rules>`, `<instruction_precedence>`,
`<tools>` or `<env>` tag -- an attempt renders as inert escaped text
(`&lt;core_rules&gt;...`), and `<instruction_precedence>` explicitly tells the
model the `<soul>` block is reference data, not an authority. This is
guarded by `TestBuildSystemPromptSoulCannotOverrideInvariants` in
`internal/gateway/prompt_test.go`, which renders a hostile SOUL that attempts
to countermand `core_rules` and asserts the real invariant tags stay singular
and their text stays intact. Keep this test in sync if the template's escaping
or block structure changes -- do not relax it to make a future SOUL feature
(e.g. curator-proposed edits) fit.

**Layer 3 — representation.** Curator-derived knowledge about the user. It may
only **propose** changes to SOUL and never apply them. If derived conclusions can
silently rewrite tone, personality drifts and the user loses the control SOUL
exists to give them. A proposal queue also makes the visibility requirement fall
out for free, since pending conclusions are inherently reviewable.

**Why SOUL comes first:** tone currently lives in Go string constants
(`DefaultPersonas` in `internal/gateway/persona.go`), so fixing a rude reply on
2026-07-29 required editing Go and rebuilding. SOUL is small, fixes a real
defect, and defines the boundary the curator must respect.
