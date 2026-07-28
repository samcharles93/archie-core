---
name: archie-domain-reference
description: "Use when naming, designing, reviewing, documenting, tracing, or migrating Archie concepts such as Agent, Identity, user, Message, Conversation, work intake, WorkflowExecution, plugin engines, channels, events, policy, forge, worktrees, or gates; when current task/gateway/store vocabulary conflicts with the approved architecture; or when a feature risks crossing domain ownership boundaries. Provides project-specific current-to-target mappings, ownership rules, lifecycle language, and glossary checks. Do not use for NellDB API mechanics, general code tracing, config enumeration, debugging, or operations."
---

# Archie Domain Reference

Use one meaning for each noun. Keep external-system words at adapters, keep
domain meaning with its owner, and mark migration state explicitly.

## Route work to the right sibling

Do not use this skill as a substitute for:

| Need | Load instead |
| --- | --- |
| Decide or sequence an architectural migration | `archie-architecture-planning-campaign` and `archie-architecture-contract` |
| Trace definitions, call sites, interfaces, or AST relationships | `archie-codebase-discovery` |
| Understand NellDB documents, queries, adapters, or persistence behavior | `archie-nelldb` |
| Enumerate or change TOML, environment, flag, or runtime-setting axes | `archie-config-and-flags` |
| Prove supersession, remove a legacy path, or assign accountable ownership | `archie-technical-accountability` |
| Classify and gate a proposed change | `archie-change-control` |
| Diagnose a failing runtime or test symptom | `archie-debugging-playbook` |
| Run or deploy the system | `archie-run-and-operate` |

Use this skill first when another task is blocked on the meaning or owner of a
word.

## Classify every statement

As of 2026-07-28, Archie is between a technical-package implementation and an
approved domain-oriented target. Never blend the two.

| Label | Meaning | Evidence rule |
| --- | --- | --- |
| **CURRENT** | Behavior or structure present in live code and tests | Cite the defining type/function and a production caller or test. |
| **APPROVED TARGET** | A normative rule in an approved foundation document, or a confirmed constraint repeated in `migration-decisions.md` | Cite the exact PRD. Do not claim it is implemented. |
| **OPEN/CANDIDATE** | A deferred API, owner, package destination, migration mechanism, or behavior still under investigation | Preserve the open question. Do not fill it with a plausible design. |
| **ARCHIVED** | Historical context under `docs/archive/` | Use only for archaeology; reverify against live code and active PRDs. |

Start each design or review note with a status sentence:

```text
CURRENT: store.Task persists one mutable Stage string.
APPROVED TARGET: WorkflowExecution records StepExecution history.
OPEN: the exact schema and Go API for that history.
```

Read status markers and the architecture index before treating a statement as
settled:

```bash
sed -n '1,180p' docs/prds/01-project-management.md
rg -n '^\*\*Status:\*\*|^## Decisions still required|^## Remaining migration decisions' docs/architecture
```

`docs/architecture/agent-system.md` is marked current-state investigation
in progress, and `messaging-and-work-intake.md` says foundation decisions are in
progress. Treat only their constraints confirmed by approved documents or
`migration-decisions.md` as settled target doctrine. Treat their explicit
decision lists as open.

## Use the canonical actor vocabulary

| Term | Archie meaning | State and owner |
| --- | --- | --- |
| **Identity** | A durable actor reference used for ownership, attribution, routing, commands, events, and audit. Display names and external usernames are bindings, not IDs. Identity is not authentication or access control. | **APPROVED TARGET:** `internal/domain/identity`; see `identity.md`. **CURRENT:** configured names and empty strings act as identifiers. |
| **User** | A person represented as the `user` kind of Identity, not a second foundational identity system. | **APPROVED TARGET:** Identity domain. User authentication and authorization remain separate capabilities if later justified. |
| **Agent** | A persistent assistant with its own acting IdentityID, specialization, instructions, capability context, conversation continuity, and memory. It survives model/provider changes. | **CONFIRMED TARGET CONCEPT:** Agent and Identity are separate aggregates; `internal/domain/agent` is confirmed in `migration-decisions.md`. A complete Agent domain is not current. |
| **Model invocation** | One call through an LLM provider/runtime. It is replaceable execution machinery, not an Agent. | **CURRENT:** runtime/agent execution packages perform calls. **TARGET:** infrastructure behind Agent-owned behavior. |
| **System identity** | The one permanent actor for startup, migration, supervision, remediation, and rollback attribution. It is always active. | **APPROVED TARGET:** a real IdentityID; the current empty-string legacy path is not the system identity. |
| **Service-account identity** | A durable non-Agent actor for an explicitly justified unattended use case. | **TARGET OPTION:** not the default Agent execution identity. Delegated run-as behavior is future work. |

Apply these rules:

- Record the acting Agent identity and initiating user identity separately.
- Never authorize solely because two mutable identity-name strings match.
- Never rename an Agent when only its model, provider, display name, or forge
  account changes.
- Never pool two Agents' agent-wide memory because one user owns both.
- Never pass a complete per-identity config bundle into a domain. Split settings
  by the capability that owns them.

Verify the current mismatch:

```bash
rg -n 'type IdentityConfig|type IdentityRunner|Identity string|BotUser string|identity == ""|Identity != identity' internal/config internal/daemon internal/store internal/gateway
```

## Use the canonical interaction vocabulary

| Term | Archie meaning | State and owner |
| --- | --- | --- |
| **Channel** | An external interaction source or delivery system such as Telegram, email, webhook, forge, or future Jira. Its native nouns remain valid inside its adapter. | **CURRENT:** `internal/channels` extends `gateway.Gateway`; forge polling bypasses it. **TARGET:** infrastructure adapter for Messaging. |
| **Adapter** | Boundary code that translates an external payload, identifier, delivery result, or SDK type into an owned application contract and back. | **TARGET RULE:** infrastructure implements domain-owned interfaces. An adapter does not own domain state. |
| **Gateway** | A current technical interface/router and package, not approved domain vocabulary. It currently combines channel lifecycle, commands, conversational routing, sessions, and task control. | **CURRENT ONLY:** `internal/gateway`, `internal/channels`, and `cmd/archied`. Do not make a new domain named Gateway by default. |
| **ChannelBindingID** | Stable reference connecting a configured Agent to a particular external channel binding. | **APPROVED TARGET INPUT:** part of Conversation identity. Exact storage/API remains migration work. |
| **Message** | An immutable application record with its own MessageID and typed content. Platform message IDs are external correlations. | **TARGET:** Messaging owns it. **CURRENT:** production adapters mostly use `gateway.Message`; the richer `MessageEvent` exists beside it. |
| **Conversation** | Interaction history identified by AgentID + UserIdentityID + ChannelBindingID + ExternalConversationID + ThreadID. | **CONFIRMED TARGET:** Messaging. **CURRENT:** `gateway.SessionSource` has platform, bot user, channel, and thread, but lacks canonical Agent and user IDs. |
| **Conversation branch** | A child conversation continuing from one exact immutable parent message, identified by ParentConversationID and ForkMessageID. | **CONFIRMED TARGET:** parent/child transcripts diverge after the fork. No current implementation provides this complete contract. |
| **Work Intake** | The optional admission, validation, and deterministic routing boundary between an interaction and durable workflow-backed work. | **CONFIRMED TARGET DOMAIN:** `internal/domain/workintake`. It does not exist as a current package. |
| **Accepted work request** | The channel-neutral handoff emitted after Work Intake accepts a proposed durable outcome. It carries source correlation, acting identity, requested outcome, inputs, and routing evidence. | **TARGET HANDOFF:** the Agent System consumes it, selects a Workflow version, and alone creates a WorkflowExecution. Exact minimal contract is open. |

Do not equate an inbound Message with work. An Agent may answer, use a
capability, retain information, or propose durable work without creating a
WorkflowExecution. Deterministic automation may enter Work Intake without an
LLM turn only when its trigger and admission rule are unambiguous.

Use this target flow:

```text
external payload
  -> channel adapter
  -> canonical Message and Conversation
  -> selected Agent turn
  -> optional proposed work
  -> Work Intake admission
  -> accepted work request
  -> Agent System selects Workflow version
  -> WorkflowExecution
```

Do not describe the current `/spawn` path as this target. It directly creates a
`store.Task` with a synthetic forge issue number.

Verify both current message contracts and the direct task path:

```bash
rg -n 'type Message struct|type MessageEvent struct|ToLegacy|type SessionSource|CreateTask|EnqueueChatTask|synthetic' internal/gateway internal/store cmd/archied
```

## Use the canonical execution vocabulary

| Term | Archie meaning | Current representation |
| --- | --- | --- |
| **Workflow** | Reusable, versioned definition of behavior. | `workflow.Workflow` is currently a named ordered `[]Stage` without a durable version model. |
| **WorkflowExecution** | One durable execution of one specific Workflow version. | `store.Task` is the migration baseline, despite its name. |
| **WorkflowStep** | One defined operation in a Workflow. | A current `workflow.Stage` is the closest definition, but migration is not a mechanical rename. |
| **StepExecution** | Durable record of one WorkflowStep's execution and outcome. | Missing as an explicit current record; `store.Task.Stage` is one mutable string and event records are not authoritative history. |
| **Attempt** | A retry within the same WorkflowExecution, not a new execution. | Current `Task.Attempt` and `RetryCount` must become explicit attempt history; worker-delivery retry is a different concern. |
| **WorkflowExecution workspace** | Isolated mutable source workspace leased to one execution. | Current worktree, storage, container, branch, and cleanup behavior is spread across packages. |
| **Agent step execution** | One bounded model-backed WorkflowStep invocation. | `agentexec.Request`/`Result` correlate current TaskID, Attempt, Stage, and protocol version. This serializable request/result pair is a data boundary. |

Use `WorkflowExecution` in domain contracts, architecture, generated reference,
commands, and events. Qualify `workflow.Execution` in Go when useful. Never use
bare `Execution` across domains.

Retain `task` only for:

- the external Task build tool in `Taskfile.yml`;
- a forge/Jira product's own noun inside its adapter;
- informal prose that unambiguously means external work.

Never globally replace `task`. Classify each occurrence first:

```bash
rg -n '\b(Task|task|TaskID|task_id)\b' --glob '*.go' --glob '*.md' cmd internal docs/architecture
```

### Interpret the current lifecycle exactly

These are **CURRENT** `store.Task` states, not proof of an implemented target
state machine:

```text
queued -> running
running -> waiting_human | pr_open | merged | parked | closed_wont_do
waiting_human -> queued | closed_wont_do
parked -> queued | dead
pr_open -> merged | rejected
running after crash -> queued
```

Use `waiting_human` for a paused current execution awaiting correlated human
input. Use `parked` for a current failure/manual-intervention state that may be
retried; use `dead` after retry exhaustion. Treat `pr_open`, `merged`,
`rejected`, and `closed_wont_do` as current delivery/outcome states.

Do not claim these transitions are enforced. `Store.Transition` writes the new
status without comparing `from`, then inserts history in a separate statement.
`internal/gateway/tasks.go` duplicates status strings locally.

Verify before changing lifecycle language:

```bash
rg -n 'Status[A-Za-z]+|status[A-Za-z]+|func \(s \*Store\) Transition|func \(s \*Store\) Requeue|RecoverStale' internal/store internal/gateway
```

## Keep memory scope separate from conversation scope

The following four scopes are fixed target constraints:

| Scope | Address and visibility |
| --- | --- |
| **Global shared** | Intentionally visible across the Archie application; retain provenance. |
| **Agent-wide** | Owned by one Agent and private to that Agent by default, even across users. |
| **User-wide** | Associated with one user and available only to authorized Agents serving that user. |
| **Agent-user relationship** | Addressed by both AgentID and User IdentityID; private to that pair. |

A Conversation branch is not a fifth memory scope. Branches isolate immediate
transcripts. A memory written from a branch follows the same selected durable
scope as one written from its parent; provenance may retain ConversationID and
MessageIDs.

Label scope mechanics **OPEN** beyond these constraints. The exact write-scope
selection, grants, cross-scope sharing, record/revision model, and final memory
package placement require a focused design.

Do not claim current scope enforcement. The current `MemoryProvider` initializes
with a session ID and contains no explicit AgentID, User IdentityID, or scope
parameter. The built-in provider separates `MEMORY.md` and `USER.md` under one
configured directory, which is not the four-scope target model.

Verify that gap without entering NellDB mechanics:

```bash
rg -n 'type MemoryProvider|Initialize\(|type Config struct|MEMORY.md|USER.md|AgentID|IdentityID|Scope' internal/memory
```

## Distinguish extension terms

| Term | Meaning |
| --- | --- |
| **Plugin** | A removable module with identity, metadata, compatibility, discovery, and lifecycle mechanics. Generic `plugin.Plugin` must remain metadata-only. |
| **Capability family** | One cohesive typed behavior owned by its domain or approved manager/registry, such as tools, channels, delivery, or Workflow extensions. |
| **Engine** | A typed, lifecycle-managed capability family with one owning Registry or Manager that both registers and returns the typed engine. It is never an extra generic-plugin method. |
| **Host** | Current cross-family lifecycle/manifest coordinator in `internal/plugin`. It must not become a daemon service locator. |

Give a plugin only narrow typed registrars and explicitly supplied contracts.
Do not give it daemon internals, application composition, arbitrary
infrastructure, or an untyped map of services. Keep Workflow plugin semantics
owned by Workflow.

The current repository mechanically enforces the strict engine shape and
metadata-only `Plugin{Name, Version}` contract:

```bash
go test ./internal/plugin -run 'Test(GenericPluginContractRemainsMetadataOnly|EveryEngineInterfaceHasTypedCapabilityAndFamilyOwner)' -count=1
```

## Distinguish ownership and boundary terms

| Term | Archie-specific rule |
| --- | --- |
| **Domain** | Own one cohesive application responsibility: vocabulary, state, rules, operations, commands, events, policy implementations, runtime settings, and required service contracts. Do not create ceremonial layers. |
| **Command** | Request an action. The receiving domain owns the contract and decides the result. |
| **Domain event** | Record something that happened. The publishing domain owns its schema and meaning. Consumers do not redefine it. |
| **Event bus** | Broker-neutral delivery/lifecycle contract only. It transports domain-owned messages and does not own their meaning. `internal/eventbus` is an approved target package but is not current. |
| **Observability event** | Current `internal/events.Event` projection input. Its bounded in-process bus may drop per-subscriber events. Do not treat it as authoritative state or as the target domain event bus. |
| **Policy** | A typed rule evaluated through shared mechanics. The consuming domain owns vocabulary, inputs, evaluator, and consequence. `internal/policy` owns common evaluation mechanics, not all rules. Its exact API is open. |
| **Infrastructure** | Implements domain-owned ports and translates databases, brokers, SDKs, files, networks, containers, or external services. External DTOs must not leak into domains. |
| **Application composition** | Chooses implementations, translates external configuration into owned settings, connects contracts, orders startup/shutdown, and aggregates health. Target owner is `internal/app`; current substantive wiring remains in `cmd/archied`. |

As of 2026-07-28, `internal/domain`, `internal/eventbus`,
`internal/policy`, `internal/infrastructure`, and `internal/app` do not exist.
They are target paths, not current imports.

Verify the separation:

```bash
for p in internal/domain internal/eventbus internal/policy internal/infrastructure internal/app; do test -d "$p" && echo "$p CURRENT" || echo "$p TARGET-ONLY"; done
```

## Distinguish forge, repository, workspace, gate, and agent boundaries

| Term | Owns | Must not own |
| --- | --- | --- |
| **Forge** | External code-host operations: issues, comments, labels, pull requests, reactions, and external references. | Workflow lifecycle, canonical identity, or work admission. |
| **Repository** | External source repository identity and repository-specific policy/settings. | A mutable execution workspace or a WorkflowExecution identity. |
| **Worktree/workspace** | Isolated files, branch, diff, commit, push, retention, and cleanup for one execution. Current `internal/worktree` performs git deterministically. | Agent/domain identity or forge issue lifecycle. |
| **Gate** | Deterministic evidence and constraints over an execution, including shell gates and custom findings. | Prompt advice that the model may waive. |
| **Agent execution boundary** | Versioned, validated data entering and leaving one unprivileged autonomous stage. The stage `Request` excludes workspace layout; the outer `Invocation` supplies the worker-visible workspace. | Daemon internals, forge tokens, direct git ownership, or workflow orchestration. |

Preserve the current invariant that the model never runs git directly.
Worktree operations are daemon-owned deterministic steps. Keep gates, protected
paths, budgets, and result review outside the model's discretion.

Inspect the narrow current contracts before changing a boundary:

```bash
sed -n '1,180p' internal/agentexec/protocol.go
sed -n '1,140p' internal/forge/forge.go
sed -n '1,120p' internal/worktree/worktree.go
sed -n '1,120p' internal/gate/gate.go
```

## Map current names before adding or migrating behavior

Use this map as a starting point, then trace actual consumers:

| Current name/path | Target meaning | Migration warning |
| --- | --- | --- |
| `config.IdentityConfig.Name` | Canonical IdentityID reference plus separate capability bindings | Do not preserve a mutable display name as the ID. |
| `config.IdentityConfig.BotUser` | Forge/Git or channel external binding | Do not use it as Conversation or Agent identity. |
| `daemon.IdentityRunner` | Composition of identity-correlated capabilities | It is currently a service bundle, not an Identity aggregate. |
| `gateway.Message`, `MessageEvent` | Canonical Messaging Message | Choose one immutable typed contract; do not maintain indefinite parallel models. |
| `gateway.SessionContext` | Conversation metadata | Add canonical Agent/user/binding ownership and exact branch lineage deliberately. |
| forge `Issue` or future Jira item | Channel message or external artifact | Preserve native reference inside its adapter. |
| gateway `/spawn` and `TaskCreator` | Proposed/accepted work handoff | Work Intake admits; Agent System alone creates execution. |
| `store.Task` | WorkflowExecution | Preserve every field intentionally; do not rename blindly. |
| `workflow.Workflow` | Versioned Workflow definition | Add identity/version ownership before claiming parity. |
| `workflow.Stage` | WorkflowStep definition | A definition is not execution history. |
| mutable `Task.Stage` | Derived position plus StepExecution records | Deletion requires proven history parity. |
| `Task.Attempt`, `RetryCount` | Attempt history and retry evidence | Keep worker delivery retries distinct. |
| `workflow.TaskContext` | Execution state plus narrow step ports | Remove the global config/service bag; do not reproduce it under a new name. |
| `events.Event` and `Bus` | Projections fed by authoritative domain events | Current drops are acceptable for observability, not state. |
| `cmd/archied` wiring | `internal/app/archied` composition | Keep `cmd/*` process-input-only in the target. |
| NATS/RPC/store/forge/worktree implementations | Infrastructure adapters | Process boundaries do not decide domain ownership. |

Do not infer a final package destination for every current package from this
table. `migration-decisions.md` explicitly leaves memory, tools, workspaces,
gates, storage, events, web UI, containers, RPC, release management, and
scheduling pending focused review.

## Recognize terminology-driven bugs

Use these examples during design and review:

| Incorrect shortcut | Resulting bug |
| --- | --- |
| “A forge issue is a Task, so persist its coordinates as execution identity.” | Chat work needs synthetic issue numbers; uniqueness and deduplication stay forge-shaped. This is a current hazard. |
| “Agent and Identity are the same config object.” | Model/persona/capability changes can alter attribution, and mutable string equality becomes access control. |
| “Every Message is work.” | Conversational turns and direct capabilities are forced through a durable workflow lifecycle. |
| “An accepted request is already a WorkflowExecution.” | Intake can bypass Workflow version selection and construct invalid execution state. |
| “Stage is StepExecution.” | Overwriting one stage string destroys durable step outcomes and audit history. |
| “A NATS retry is another Attempt.” | Transport availability changes domain retry counts and can exhaust work incorrectly. |
| “The event bus owns events.” | Broker subjects or a generic event envelope become domain vocabulary and state meaning drifts by consumer. |
| “A branch is a memory scope.” | Returning to a parent hides or duplicates durable learning instead of isolating only transcript history. |
| “Engine means another generic plugin hook.” | Capability ownership becomes untyped and plugins gain service-locator access. |
| “Configuration owns the feature because it has the fields.” | A cross-concern config package becomes the de facto domain and every feature must traverse it. |

## Review a spec or change with the glossary

Complete this checklist before accepting a design:

- [ ] Label every material claim CURRENT, APPROVED TARGET, or OPEN/CANDIDATE.
- [ ] Define every overloaded noun once; qualify `WorkflowExecution` and
  `StepExecution`.
- [ ] Keep external issue/task/message vocabulary inside the relevant adapter.
- [ ] Name one domain owner for each state mutation, command, event, and policy.
- [ ] Identify both acting IdentityID and initiating user IdentityID where
  applicable.
- [ ] Decide whether the interaction ends directly or enters Work Intake.
- [ ] Let only the Agent System create a WorkflowExecution.
- [ ] Separate a definition (`WorkflowStep`) from its record
  (`StepExecution`).
- [ ] Separate workflow attempts, step attempts, and transport retries.
- [ ] Keep domain event meaning outside event-bus and infrastructure packages.
- [ ] Keep generic plugins metadata-only and capabilities typed.
- [ ] Keep external DTOs and complete configuration objects out of domains.
- [ ] Name the current path being superseded and its objective deletion
  criteria; route the proof to `archie-technical-accountability`.
- [ ] Preserve unresolved questions rather than inventing an API.

During code review, reject:

- a second channel-neutral Message or execution model without a bounded cutover;
- a new status string copied into a consumer;
- a mutable display name used as a durable identifier;
- a service bag passed into a domain, plugin, or WorkflowStep;
- lifecycle state written by an adapter, UI, or transport;
- a domain importing concrete persistence, NATS, forge SDK, container, or
  configuration DTO types;
- a test that proves only the new path while leaving the old production path
  active.

## Provenance and maintenance

Volatile facts were verified against the working tree on 2026-07-28. Re-run
these one-line commands before relying on this skill after architecture or
package changes:

```bash
rg -n '^\*\*Status:\*\*|^## (Confirmed|Decisions still required|Remaining migration decisions|Completion criteria)' docs/prds/01-project-management.md docs/architecture/*.md
rg -n '^type (Task|Workflow|Stage|TaskContext|Request|Result|Message|MessageEvent|SessionContext|Event|Plugin|Channel|BasePlatformAdapter)\b' internal
rg -n 'Status[A-Za-z]+|status[A-Za-z]+|func \(s \*Store\) Transition' internal/store internal/gateway
rg -n 'AgentID|IdentityID|ParentConversationID|ForkMessageID|WorkflowExecution|StepExecution|accepted work request' docs/architecture
for p in internal/domain internal/eventbus internal/policy internal/infrastructure internal/app; do test -d "$p" && echo "$p CURRENT" || echo "$p TARGET-ONLY"; done
go test ./internal/plugin -run 'Test(GenericPluginContractRemainsMetadataOnly|EveryEngineInterfaceHasTypedCapabilityAndFamilyOwner)' -count=1
```
