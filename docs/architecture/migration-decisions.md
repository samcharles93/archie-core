# Architecture Migration Decisions

**Status:** Active migration inventory  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Purpose

This document records what remains to be decided before Archie's current
implementation can be migrated into the approved domain-oriented structure.

The product and core domain model are not being redesigned. Existing Archie
behaviour and the existing session implementation are the migration
baseline. Remaining decisions MUST be grounded in current packages, consumers,
tests, persistence, and runtime wiring.

## Confirmed domain packages

```text
internal/domain/
  identity/
  agent/
  workflow/
  messaging/
  workintake/
  plugin/
```

- `identity` owns persistent actors, lifecycle, ownership, attribution, and
  audit identity.
- `agent` owns persistent assistants, specialisation, instructions,
  model-independent continuity, capability context, and associations with
  identity and memory.
- `workflow` is a separate domain. It owns reusable, versioned Workflow
  definitions, WorkflowSteps, WorkflowExecutions, StepExecutions, Attempts,
  outcomes, and typed Workflow plugin contracts.
- `messaging` owns canonical Messages, Conversations, branches, channel and
  source correlation, replies, acknowledgements, delivery attempts, events, and
  projections.
- `workintake` owns admission, validation, deterministic routing, and accepted
  work requests for the optional transition into durable workflow-backed
  execution.
- `plugin` owns generic plugin identity, discovery, compatibility, and lifecycle
  mechanics. It remains metadata-only; capability semantics and typed
  registration belong to the owning domain.

Memory's final package placement remains undecided pending focused review of the
existing implementation.

The following are approved shared or composition packages, not domains:

```text
internal/eventbus/
internal/policy/
internal/infrastructure/
internal/app/archied/
internal/app/agentworker/
```

## Remaining migration decisions

### 1. Complete package destination map

Every current package MUST receive one explicit disposition:

- move into a confirmed domain;
- move under infrastructure as an implementation of a domain-owned contract;
- move into application composition;
- merge with an identified duplicate;
- remain as an approved shared application contract;
- move to a plugin, extra, deployment, example, or generated artifact;
- be deleted after its replacement is proven.

The largest unreviewed areas include memory, tools and capabilities, workspace
and worktree behaviour, gates, storage, events, web UI, containers, RPC
packages, release management, and scheduling.

The destination matrix MUST name the behaviour owner, target path, dependencies
to remove, state and contracts to preserve, migration prerequisites, and
deletion criteria.

### 2. Session and Messaging migration

The existing session implementation is the migration baseline. The migration must
decide:

- which session and message components are retained or adapted;
- how Agent, user, channel, Conversation, and branch ownership are added;
- how Archie's parallel gateway session implementation is replaced;
- how canonical Messages become immutable persisted records;
- how existing NellDB sessions and messages are migrated;
- how outbound delivery, retry, acknowledgement, deduplication, and failure
  semantics are represented;
- how current Telegram, email, webhook, forge, and Jira integrations migrate to
  Messaging infrastructure adapters.

The following are already decided and MUST NOT be reopened without contradictory
implementation evidence:

- the composite Conversation identity;
- immutable MessageIDs;
- typed tool-call and tool-result messages;
- exact branch lineage through ParentConversationID and ForkMessageID;
- parent and child transcript isolation;
- normal scoped-memory behaviour regardless of the originating Conversation;
- Messaging and Work Intake ownership.

### 3. Identity data migration

The migration must define:

- the durable identity schema and repository implementation;
- bootstrap of the permanent system identity;
- stable IdentityIDs for existing configured Agents;
- migration from configured identity names, empty-string ownership, and bot
  usernames;
- preservation of historical attribution;
- identity-aware uniqueness and transport deduplication;
- correction of container and RPC capability resolution so execution uses the
  owning identity's bindings;
- removal of identity-string equality from access-control behaviour.

Exact schema and method signatures are implementation decisions. The approved
Identity lifecycle and attribution semantics are fixed migration constraints.

### 4. Workflow migration

The current implementation must be transformed deliberately:

```text
store.Task            -> WorkflowExecution
mutable Stage         -> StepExecution history
RetryCount / Attempt  -> Attempt records
workflow.Registry     -> versioned Workflow definitions
current stages        -> WorkflowStep definitions or Workflow plugins
```

The migration must decide:

- Workflow definition and version persistence;
- the smallest accepted-work-request contract consumed from Work Intake;
- the enforced WorkflowExecution state machine;
- atomic state and domain-event persistence;
- migration of existing task rows and transition history;
- disposition of current skill, Yaegi, and stage-plugin implementations;
- the versioned worker-handoff contract;
- scheduler, claim, retry, crash-recovery, and waiting-human cutover.

Workflow remains a separate domain from Agent. A user or Agent may define or
invoke a Workflow, but Workflow semantics and plugins remain Workflow-owned.

### 5. Memory placement and storage

The four required scopes are fixed:

- global shared;
- Agent-wide;
- user-wide;
- Agent-user relationship.

A focused review of `internal/memory`, its providers, consumers, persistence,
and tests must decide:

- `internal/domain/memory` versus ownership within the Agent domain;
- the authoritative memory record and revision model;
- provider and infrastructure boundaries;
- retrieval and access enforcement;
- migration of existing memory data;
- provenance representation.

Conversation branches do not create another memory scope. A memory action has
the same scoped effect regardless of which Conversation originated it.

### 6. Runtime and process boundaries

The migration must decide which current execution paths survive:

- in-process execution;
- `archie-agent` subprocess execution;
- NATS worker execution;
- container isolation;
- forge, store, and worktree RPC services.

The source architecture remains a modular monolith. A retained process boundary
requires a concrete security-isolation, failure-containment, scaling, or
operational justification. Possible future extraction is insufficient.

### 7. Shared mechanics

The following cross-domain mechanics require exact contracts before dependent
packages migrate:

- event-bus delivery and failure semantics;
- policy evaluation API, composition, and precedence;
- configuration candidate preparation, health validation, atomic promotion,
  observation, and rollback;
- service-specific health, bounded remediation, isolation, and escalation;
- changelog association and grouping.

These shared mechanisms do not acquire ownership of domain meaning. Domains
continue to own their commands, events, policies, settings, and consequences.

The copied Go `specgen` and VitePress documentation pipeline, output placement,
developer commands, drift checking, hook sequence, and CI sequence are defined
in `generated-documentation.md`. Adapting the copied files, implementing
Archie's normalized documentation model and page renderers, and cutting `docs`
over to its final location remain migration work rather than open product
architecture.

### 8. Cutover sequence

The final migration plan must order:

- compatibility adapters;
- schema migrations and data backfills;
- any required dual-read or transitional paths;
- domain-by-domain package replacement;
- production wiring changes;
- legacy package removal;
- architecture tests preventing dependency regression;
- focused, integration, race, and full quality gates.

Each legacy package requires objective removal criteria. No compatibility path
may become an indefinite second implementation.

## Completion criteria

Migration design is complete when:

- every current package has one approved owner and destination;
- every mutable record has one authoritative owner and migration path;
- all external and cross-process contracts have versioned owners;
- existing behaviour and data have explicit parity requirements;
- process boundaries have evidence-backed justification;
- the cutover order preserves a working application;
- legacy deletion criteria and deterministic architecture tests are defined;
- no unresolved decision requires an implementer to invent domain semantics.

Exact Go APIs, SQL statements, and mechanical refactoring steps MAY be finalized
during implementation when they do not change these semantics.
