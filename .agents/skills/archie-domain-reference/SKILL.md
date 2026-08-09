---
name: archie-domain-reference
description: "Use when naming, designing, reviewing, documenting, tracing, or migrating Archie concepts such as Agent, Identity, user, Message, Conversation, work intake, WorkflowExecution, plugin engines, channels, events, policy, forge, worktrees, or gates; when current task/gateway/store vocabulary conflicts with the approved architecture; or when a feature risks crossing domain ownership boundaries. Provides project-specific current-to-target mappings, ownership rules, lifecycle language, and glossary checks. Do not use for database API mechanics, general code tracing, config enumeration, debugging, or operations."
---

# Archie Domain Reference

Use one meaning for each noun. Keep external-system words at adapters, keep
domain meaning with its owner, and mark migration state explicitly.

## Classify every statement

As of 2026-07-28, Archie is between technical-package implementation and
approved domain-oriented target. Never blend the two.

| Label | Meaning | Evidence rule |
| --- | --- | --- |
| **CURRENT** | Behavior present in live code and tests | Cite defining type/function and production caller or test. |
| **APPROVED TARGET** | Normative rule in approved foundation document | Cite exact PRD. Do not claim implemented. |
| **OPEN/CANDIDATE** | Deferred API, owner, package, migration, or behavior | Preserve open question. |
| **ARCHIVED** | Historical context under `docs/archive/` | Use only for archaeology. |

Start design notes with status sentences:

```text
CURRENT: store.Task persists one mutable Stage string.
APPROVED TARGET: WorkflowExecution records StepExecution history.
OPEN: the exact schema and Go API for that history.
```

Read status markers:

```bash
sed -n '1,180p' docs/prds/01-project-management.md
rg -n '^\*\*Status:\*\*|^## Decisions still required' docs/architecture
```

## Use the canonical actor vocabulary

| Term | Archie meaning | State and owner |
| --- | --- | --- |
| **Identity** | Durable actor reference for ownership, attribution, routing, commands, events, audit. | **APPROVED TARGET:** `internal/domain/identity`. **CURRENT:** configured names and empty strings act as identifiers. |
| **User** | Person represented as `user` kind of Identity. | **APPROVED TARGET:** Identity domain. |
| **Agent** | Persistent assistant with acting IdentityID, specialization, instructions, capability context, memory. | **CONFIRMED TARGET:** Agent and Identity separate aggregates; `internal/domain/agent` confirmed in `migration-decisions.md`. |
| **Model invocation** | One call through LLM provider/runtime. Replaceable execution machinery. | **CURRENT:** runtime packages. **TARGET:** infrastructure behind Agent-owned behavior. |
| **System identity** | One permanent actor for startup, migration, supervision, rollback. | **APPROVED TARGET:** a real IdentityID. |

Apply: record acting Agent identity and initiating user identity separately;
never authorize because two mutable strings match; never rename Agent when only
model/provider/display name changes; never pool two Agents' memory.

```bash
rg -n 'type IdentityConfig|type IdentityRunner|Identity string|BotUser string' internal/config internal/daemon internal/store internal/gateway
```

## Use the canonical interaction vocabulary

| Term | Archie meaning | State and owner |
| --- | --- | --- |
| **Channel** | External interaction source (Telegram, email, webhook, forge). | **CURRENT:** `internal/channels` extends `gateway.Gateway`. **TARGET:** infrastructure adapter for Messaging. |
| **Adapter** | Boundary code translating external payloads into owned contracts. | **TARGET RULE:** infrastructure implements domain-owned interfaces. |
| **Gateway** | Current technical interface/router and package — not approved domain vocabulary. | **CURRENT ONLY:** `internal/gateway`, `internal/channels`. |
| **ChannelBindingID** | Stable reference connecting Agent to channel binding. | **APPROVED TARGET INPUT:** part of Conversation identity. |
| **Message** | Immutable application record with MessageID and typed content. Platform IDs are external correlations. | **TARGET:** Messaging. **CURRENT:** `gateway.Message`; richer `MessageEvent` exists beside it. |
| **Conversation** | AgentID + UserIdentityID + ChannelBindingID + ExternalConversationID + ThreadID. | **CONFIRMED TARGET:** Messaging. **CURRENT:** `gateway.SessionSource` lacks canonical Agent/user IDs. |
| **Conversation branch** | Child conversation from one immutable parent message. | **CONFIRMED TARGET.** No current implementation. |
| **Work Intake** | Admission, validation, routing boundary between interaction and durable workflow-backed work. | **CONFIRMED TARGET:** `internal/domain/workintake`. Does not exist as current package. |
| **Accepted work request** | Channel-neutral handoff after Work Intake accepts proposed outcome. | **TARGET HANDOFF:** Agent System consumes it, selects Workflow version, creates WorkflowExecution. |

Do not equate an inbound Message with work. An Agent may answer, use
capabilities, retain information, or propose work without creating a
WorkflowExecution.

Target flow:

```text
external payload -> channel adapter -> canonical Message and Conversation
-> selected Agent turn -> optional proposed work -> Work Intake admission
-> accepted work request -> Agent System selects Workflow version -> WorkflowExecution
```

Do not describe current `/spawn` as this target. It directly creates a
`store.Task` with synthetic forge issue number.

```bash
rg -n 'type Message struct|type MessageEvent struct|ToLegacy|type SessionSource|CreateTask|EnqueueChatTask|synthetic' internal/gateway internal/store cmd/archied
```

## Use the canonical execution vocabulary

| Term | Archie meaning | Current representation |
| --- | --- | --- |
| **Workflow** | Reusable, versioned definition. | `workflow.Workflow` is named `[]Stage` without durable version model. |
| **WorkflowExecution** | One durable execution of one Workflow version. | `store.Task` is migration baseline. |
| **WorkflowStep** | One defined operation. | `workflow.Stage` is closest definition. |
| **StepExecution** | Durable record of one step's execution and outcome. | Missing; `store.Task.Stage` is one mutable string. |
| **Attempt** | Retry within same WorkflowExecution. | `Task.Attempt`/`RetryCount` must become explicit attempt history. |
| **Agent step execution** | One bounded model-backed WorkflowStep invocation. | `agentexec.Request`/`Result` correlate TaskID, Attempt, Stage, protocol version. |

Use `WorkflowExecution` in domain contracts, architecture, generated reference.
Retain `task` only for Task build tool, forge product nouns in adapters,
informal prose.

### Interpret the current lifecycle exactly

**CURRENT** `store.Task` states:

```text
queued -> running
running -> waiting_human | pr_open | merged | parked | closed_wont_do
waiting_human -> queued | closed_wont_do
parked -> queued | dead
pr_open -> merged | rejected
running after crash -> queued
```

Do not claim enforced. `Store.Transition` writes new status without comparing
`from`, inserts history separately. `internal/gateway/tasks.go` duplicates
status strings locally.

```bash
rg -n 'Status[A-Za-z]+|func \(s \*Store\) Transition|func \(s \*Store\) Requeue|RecoverStale' internal/store internal/gateway
```

## Keep memory scope separate from conversation scope

| Scope | Address and visibility |
| --- | --- |
| **Global shared** | Intentionally visible across Archie. |
| **Agent-wide** | Owned by one Agent, private by default even across users. |
| **User-wide** | One user across authorized Agents. |
| **Agent-user relationship** | Addressed by both AgentID and User IdentityID. |

Conversation branch is not a fifth memory scope. Scope mechanics **OPEN** beyond
these constraints.

Current `MemoryProvider` initializes with session ID, contains no explicit
AgentID, User IdentityID, or scope parameter. Built-in provider separates
`MEMORY.md` and `USER.md` under one directory — not the four-scope target model.

```bash
rg -n 'type MemoryProvider|Initialize\(|type Config struct|MEMORY.md|USER.md|AgentID|IdentityID|Scope' internal/memory
```

## Distinguish extension terms

| Term | Meaning |
| --- | --- |
| **Plugin** | Removable module with identity, metadata, compatibility, lifecycle. Generic `plugin.Plugin` must remain metadata-only. |
| **Capability family** | One cohesive typed behavior owned by its domain or approved manager/registry. |
| **Engine** | Typed, lifecycle-managed capability family with owning Registry or Manager. |
| **Host** | Current cross-family lifecycle/manifest coordinator in `internal/plugin`. Must not become service locator. |

Give plugin only narrow typed registrars and explicitly supplied contracts.

```bash
go test ./internal/plugin -run 'Test(GenericPluginContractRemainsMetadataOnly|EveryEngineInterfaceHasTypedCapabilityAndFamilyOwner)' -count=1
```

## Distinguish ownership and boundary terms

| Term | Archie-specific rule |
| --- | --- |
| **Domain** | Own one cohesive responsibility: vocabulary, state, rules, operations, commands, events, policies, runtime settings, required service contracts. |
| **Command** | Request an action. Receiving domain owns contract and decides result. |
| **Domain event** | Record something that happened. Publishing domain owns schema. |
| **Event bus** | Broker-neutral delivery/lifecycle contract. `internal/eventbus` is approved target, not current. |
| **Observability event** | `internal/events.Event` projection input. Bounded in-process bus may drop. Not authoritative state. |
| **Policy** | Typed rule evaluated through shared mechanics. Consuming domain owns vocabulary. |
| **Infrastructure** | Implements domain-owned ports, translates DBs, brokers, SDKs, files, containers. |
| **Application composition** | Chooses implementations, translates config, connects contracts, orders startup/shutdown. Target: `internal/app`; current wiring in `cmd/archied`. |

As of 2026-07-28, `internal/domain`, `internal/eventbus`, `internal/policy`,
`internal/infrastructure`, `internal/app` do not exist. Target paths only.

```bash
for p in internal/domain internal/eventbus internal/policy internal/infrastructure internal/app; do test -d "$p" && echo "$p CURRENT" || echo "$p TARGET-ONLY"; done
```

## Distinguish forge, repository, workspace, gate, and agent boundaries

| Term | Owns | Must not own |
| --- | --- | --- |
| **Forge** | External code-host ops: issues, comments, labels, PRs, reactions. | Workflow lifecycle, canonical identity, work admission. |
| **Repository** | External source repo identity and repo-specific policy. | Mutable execution workspace or WorkflowExecution identity. |
| **Worktree/workspace** | Isolated files, branch, diff, commit, push, cleanup for one execution. | Agent/domain identity or forge issue lifecycle. |
| **Gate** | Deterministic evidence and constraints over execution. | Prompt advice model may waive. |
| **Agent execution boundary** | Versioned, validated data entering/leaving one unprivileged stage. | Daemon internals, forge tokens, direct git, workflow orchestration. |

Model never runs git directly. Worktree operations are daemon-owned.

## Map current names before adding or migrating behavior

| Current name/path | Target meaning | Migration warning |
| --- | --- | --- |
| `config.IdentityConfig.Name` | Canonical IdentityID plus separate bindings | Do not preserve mutable display name as ID. |
| `config.IdentityConfig.BotUser` | Forge/Git or channel external binding | Not Conversation or Agent identity. |
| `daemon.IdentityRunner` | Composition of identity-correlated capabilities | Currently service bundle, not Identity aggregate. |
| `gateway.Message`, `MessageEvent` | Canonical Messaging Message | Choose one immutable typed contract. |
| `gateway.SessionContext` | Conversation metadata | Add canonical Agent/user/binding ownership. |
| forge `Issue` or future Jira item | Channel message or external artifact | Preserve native reference in adapter. |
| gateway `/spawn` and `TaskCreator` | Proposed/accepted work handoff | Work Intake admits; Agent System creates execution. |
| `store.Task` | WorkflowExecution | Preserve every field intentionally. |
| `workflow.Workflow` | Versioned Workflow definition | Add identity/version ownership before parity. |
| `workflow.Stage` | WorkflowStep definition | A definition is not execution history. |
| mutable `Task.Stage` | Derived position plus StepExecution records | Deletion requires proven history parity. |
| `Task.Attempt`, `RetryCount` | Attempt history and retry evidence | Keep worker delivery retries distinct. |
| `workflow.TaskContext` | Execution state plus narrow step ports | Remove global config/service bag. |
| `events.Event` and `Bus` | Projections fed by authoritative domain events | Current drops acceptable for observability, not state. |
| `cmd/archied` wiring | `internal/app/archied` composition | Keep `cmd/*` process-input-only. |
| NATS/RPC/store/forge/worktree | Infrastructure adapters | Process boundaries don't decide domain ownership. |

`migration-decisions.md` leaves memory, tools, workspaces, gates, storage,
events, web UI, containers, RPC, release management, scheduling pending.

## Recognize terminology-driven bugs

| Incorrect shortcut | Resulting bug |
| --- | --- |
| "A forge issue is a Task" | Chat needs synthetic issues; uniqueness/dedup stay forge-shaped. |
| "Agent and Identity are same config object" | Model/persona changes alter attribution; mutable string equality becomes access control. |
| "Every Message is work" | Conversational turns forced through durable workflow lifecycle. |
| "Accepted request is already a WorkflowExecution" | Intake bypasses version selection and constructs invalid state. |
| "Stage is StepExecution" | Overwriting one string destroys step outcomes and audit history. |
| "NATS retry is another Attempt" | Transport availability changes domain retry counts. |
| "Event bus owns events" | Broker subjects become domain vocabulary; meaning drifts by consumer. |
| "Branch is a memory scope" | Returning to parent hides/duplicates durable learning. |
| "Engine means another generic plugin hook" | Capability ownership becomes untyped; plugins gain service-locator access. |
| "Configuration owns the feature" | Cross-concern config becomes de facto domain. |

Reject in review: a second parallel model without bounded cutover; status string
copied into consumer; mutable display name as durable ID; service bag passed
into domain/plugin/WorkflowStep; lifecycle state written by adapter/UI/transport;
domain importing concrete persistence/NATS/forge SDK/container/config DTO types;
test proving only new path while old production path remains active.
