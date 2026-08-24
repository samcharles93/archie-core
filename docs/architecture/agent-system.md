# Agent System

**Status:** Current-state investigation in progress  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Domain framing

The domain is the Agent System.

`Task` is not a core Agent System concept. Forge tasks, forge issues, Jira
issues, email, Telegram messages, webhooks, and future external inputs are
channel-specific messages. Channels translate those messages into requests to
the Agent System; external issue or task models MUST NOT shape execution state.

The current `store.Task` is a persisted workflow execution. Treating that row as
a task-domain aggregate would incorrectly turn Archie into an agentic issue
board.

## Canonical agent interaction model

An **Agent** is a persistent assistant selected for an interaction. It is not a
model invocation and is not a WorkflowExecution.

Each Agent has its own:

- immutable acting `IdentityID` for ownership and audit;
- durable memory and learned information about the user;
- perspective, specialisation, instructions, and communication behaviour;
- conversation history and channel bindings;
- available tools, capabilities, and policy;
- continuity across model or provider changes.

The underlying model is a replaceable runtime dependency. Changing a model,
provider, or model version MUST NOT create a new Agent, discard its memory, or
change ownership of its history.

A user MAY create multiple Agents. Each Agent develops its own memory and view
of the user from the interactions and responsibilities assigned to it. Memories
MUST NOT be silently pooled merely because Agents belong to the same user or
application.

Agent and Identity are separate aggregates. Every Agent references exactly one
acting IdentityID, and no two Agents share an agent identity. User, system, and
service-account identities may exist without an Agent.

An action initiated through an Agent records the Agent's acting IdentityID and,
when applicable, the initiating user's IdentityID. A future fully automated
WorkflowExecution MAY use an explicitly selected service-account identity for a
concrete unattended use case; this is not the default execution identity.

## Workflow execution identity

A Workflow is owned by an IdentityID. By default, every WorkflowExecution acts
as the Workflow owner's identity.

Future delegated execution MAY add an explicit `RunAsIdentityID` assignment to a
Workflow. This allows a Workflow owner to select another user or service-account
identity for execution without changing Workflow ownership. Until that
capability is deliberately introduced, owner identity and execution identity are
the same.

WorkflowExecution audit records MUST preserve the Workflow owner, effective
execution identity, initiating user when one exists, and any explicit delegation
that selected a different run-as identity.

## Memory scopes

The Agent System requires four distinct durable memory scopes:

- **global shared memory**: information intentionally available across the
  Archie application;
- **agent-wide memory**: general expertise, operating knowledge, and perspective
  owned by one Agent and safe for that Agent to use across users;
- **user-wide memory**: information associated with one user and available to
  authorized Agents serving that user;
- **Agent-user relationship memory**: private history and learned context from
  one particular Agent's relationship with one particular user.

Agent-wide memory is private to its owning Agent by default. Another Agent MUST
NOT read, inherit, search, or mutate it implicitly.

Agent-user relationship memory is addressed by both AgentID and User IdentityID.
Neither another Agent serving the same user nor another user speaking to the
same Agent may access it implicitly.

Archie is a multi-user system even when initially deployed for one person.
Multiple users, including family members, may interact with the same configured
Agents. Per-user memory is therefore an isolation and personalization boundary,
not merely an attribution tag on the primary user's records.

An Agent MUST resolve the initiating user before assembling memory context.
Memory belonging to one user MUST NOT become visible to another user merely
because both users interact with the same Agent.

Shared memory does not erase provenance. Every memory record retains its scope,
author or acting identity, originating user when applicable, source, and
revision history. The exact rules for selecting a write scope, granting access
to global and user-wide memory, and authorizing explicit cross-scope sharing
require a focused memory design decision.

An Agent interaction may answer conversationally, use a tool or capability, read
or send messages, retain information, or propose durable workflow-backed work.
None of these operations inherently requires a WorkflowExecution.

## Agent and Workflow domain boundary

Agent and Workflow are separate domains:

```text
internal/domain/agent
internal/domain/workflow
```

The Agent domain owns persistent assistants, their specialisation, instructions,
model-independent continuity, capability context, and associations with identity
and memory.

The Workflow domain owns reusable, user- or Agent-defined behaviour and every
durable execution of that behaviour. An Agent may invoke or define a Workflow,
but the Workflow is not part of the Agent aggregate and does not require a
conversational Agent interaction to execute.

## Canonical workflow model

The Workflow feature uses these canonical terms:

- **Workflow**: a reusable, versioned definition of how an agent behaves and
  performs a kind of work.
- **WorkflowExecution**: one durable execution of a specific Workflow version.
- **WorkflowStep**: one defined operation in a Workflow.
- **StepExecution**: the recorded execution and outcome of one WorkflowStep.
- **Attempt**: a retry of the same WorkflowExecution, not a new execution.

A Workflow MAY structure any repeatable agent behaviour, including personal
assistance and scheduled routines. For example, a morning email review may
combine a schedule event, message retrieval tool, LLM classification and
summarisation, and delivery of the resulting rundown. Workflow is therefore not
restricted to forge work, source changes, or requests submitted by a user at
execution time.

Workflows are extensible through Workflow-owned plugins. The Workflow domain
defines the typed plugin registration, definition, validation, versioning, and
execution contracts. Users and Agents may define reusable Workflows through
those contracts.

The generic plugin contract remains metadata-only. It MUST NOT own Workflow
semantics or replace the Workflow registry with generic hooks.

`Workflow` and `WorkflowExecution` are the names used in architecture, generated
reference, configuration, commands, events, and external contracts. The
implementation MAY use package qualification to remain concise:

```go
workflow.Definition
workflow.Execution
workflow.Step
workflow.StepExecution
```

The unqualified name `Execution` MUST NOT be used in cross-domain contracts or
documentation because it is ambiguous among workflow, tool, command, policy, and
service execution.

## Current workflow-execution feature

The current feature takes a channel request through agent execution and a
durable outcome.

Current execution state includes source-message references, owning identity,
repository, selected workflow and stage, agent output, branch and pull-request
references, token and iteration counts, retry information, human-wait
information, and failure details.

The current `store.Task` lifecycle uses:

```text
queued -> running
running -> waiting_human | pr_open | merged | parked | closed_wont_do
waiting_human -> queued | closed_wont_do
parked -> queued | dead
pr_open -> merged | rejected
running after crash -> queued
```

Workflows select and sequence deterministic and agent-run stages. Implement,
TDD, feasibility, and bootstrap are current Workflow definitions.
Every WorkflowExecution is handed wholesale over core NATS to a task-scoped
`archie-agent` container, which owns the workflow stage loop.

## Current workflow-feature placement

The behaviour is divided across technical packages:

- `internal/store` defines the current `Task` execution record and status
  strings and persists mutable state and transition history.
- `internal/daemon` discovers, claims, schedules, retries, recovers, processes,
  waits for human input, and reconciles pull requests.
- `internal/workflow` routes work, executes stages, mutates tasks, applies
  outcomes, parks failures, and performs forge/worktree operations.
- `internal/agentexec` defines and runs individual autonomous-stage protocols.
- `internal/taskrun` hands a complete WorkflowExecution to a container worker.
- `internal/gate` and repository configuration define execution constraints.
- `internal/gateway` duplicates task status strings and applies task-control
  rules.
- forge, worktree, NATS, SQLite, containers, and agent runtimes are concrete
  dependencies mixed into orchestration.

## Current `task` meanings and required replacements

The existing code uses `task` for unrelated concepts. Migration MUST resolve
each meaning deliberately; a global text replacement is prohibited.

| Current meaning                                    | Representative locations                                        | Target concept                                           | Requirements                                                                                                                                                                                                                                                                                                                                                                      |
| -------------------------------------------------- | --------------------------------------------------------------- | -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Forge issue or external task                       | `internal/forge`, daemon polling                                | Channel message                                          | Preserve the channel's native reference and payload. Translate it into an Agent System request without importing forge or issue semantics into Workflow.                                                                                                                                                                                                                          |
| Jira issue or other external work item             | Messaging infrastructure adapter                                | Channel message or external artifact                     | The external system retains its own vocabulary inside its integration. Its record may lead to an accepted work request but is not a Workflow or WorkflowExecution.                                                                                                                                                                                                                |
| Persisted `store.Task` row                         | `internal/store`                                                | WorkflowExecution                                        | Own identity, Workflow ID and version, lifecycle, source-message reference, outputs, attempts, and outcome. Persistence implements the Agent System repository contract.                                                                                                                                                                                                          |
| Task status and transition history                 | `store.Status*`, `transitions`                                | WorkflowExecution lifecycle and domain events            | Enforce legal transitions atomically. Domain events and state changes cannot diverge. Remove duplicated status strings from consumers.                                                                                                                                                                                                                                            |
| Claimed or queued task                             | `ClaimNext`, `ClaimByIssue`, `RecoverStale`                     | Scheduled WorkflowExecution                              | Claim by WorkflowExecution ID and expected version/state. Crash recovery and leases must not silently create a new execution.                                                                                                                                                                                                                                                     |
| `workflow.TaskContext`                             | `internal/workflow`                                             | WorkflowExecution context                                | Replace the mutable service bag with execution state plus narrow step-required contracts. Complete application configuration and unrelated services are forbidden.                                                                                                                                                                                                                |
| `taskrun.Request` and `taskrun.Response`           | `internal/taskrun`; consumed by `internal/app/agentworker`, decoded by `internal/infrastructure/agenttransport/nats` | WorkflowExecution handoff                                | This is the versioned worker contract for executing a WorkflowExecution. It carries domain-owned execution data, not store rows or global configuration DTOs.                                                                                                                                                                                                                     |
| Per-stage `agentexec.Request.TaskID`               | `internal/agentexec`                                            | WorkflowExecutionID correlation                          | An agent invocation is a bounded WorkflowStep execution. Correlate request/result with WorkflowExecutionID, StepExecutionID, attempt, and protocol version.                                                                                                                                                                                                                       |
| NATS `TaskMessage` and `archie.task.*`             | `internal/nats`                                                 | Channel request or WorkflowExecution transport           | Discovery messages and execution dispatch are different contracts and subjects. Domain meaning stays outside NATS infrastructure. Identity and source-message idempotency must be explicit.                                                                                                                                                                                       |
| `taskDispatcher` work item                         | `internal/daemon`                                               | WorkflowExecution scheduling                             | Scheduling operates on execution references and declared concurrency policy. Repository serialization is policy, not identity derived from an incomplete task struct.                                                                                                                                                                                                             |
| `container.TaskPayload` and `task.json`            | `internal/container`; read by `internal/infrastructure/agentboot`        | WorkflowExecution brief                                  | Rename the boot artifact and schema. It identifies the mounted execution and required workspace only; it is not an alternative execution definition. Missing or malformed input must fail startup before the worker subscribes.                                                                                                                                                   |
| `storage.TaskRef`                                  | `internal/storage`                                              | WorkflowExecution workspace lease                        | Storage receives the minimal execution/workspace reference and policy required to provision and release resources. It does not receive the WorkflowExecution aggregate.                                                                                                                                                                                                           |
| Gateway `TaskCreator`                              | `internal/gateway`                                              | Accepted work request handoff                            | A channel may carry a request for durable workflow-backed work, but it does not request or create a WorkflowExecution directly. Work intake admits and routes the request; the Agent System consumes the accepted work request and is the sole owner of selecting a Workflow version and creating the WorkflowExecution. Ordinary agent interactions do not require this handoff. |
| Gateway `TaskController` and `ChatTaskStatus`      | `internal/gateway/tasks.go`                                     | WorkflowExecution commands and query view                | Approve, cancel, and status use Agent System contracts. Channel code does not duplicate lifecycle constants or decide transitions.                                                                                                                                                                                                                                                |
| Gateway `TaskProfile`                              | `internal/gateway/tasks.go`, `cmd/archied`                      | Channel/identity execution defaults                      | This is configuration selecting allowed source targets and default Workflow inputs, not task state. Split it into settings owned by the relevant channel and Agent System features.                                                                                                                                                                                               |
| `config.TaskConfig` and `TaskForge`                | `internal/config`                                               | Worker execution settings and channel/service references | Dissolve with `internal/config`. Each field moves to its owning runtime settings or versioned WorkflowExecution handoff contract. Secrets and external configuration forms do not enter the domain.                                                                                                                                                                               |
| `events.Event.TaskID`, task event timelines        | `internal/events`, stores, web UI                               | WorkflowExecutionID and WorkflowExecution events         | Authoritative events are Agent System events. Observability and timelines project those events rather than inventing parallel generic task events.                                                                                                                                                                                                                                |
| Web UI task lists and counts                       | `internal/webui`, store query APIs                              | WorkflowExecution read model                             | Dashboard status, history, and counts are projections of WorkflowExecution state and events. The UI does not depend on persistence records.                                                                                                                                                                                                                                       |
| Task-scoped forge/store/worktree RPC               | `forgerpc`, `storerpc`, `worktreerpc`, `registerTaskRPCServers` | Execution-scoped capability ports                        | Worker calls use narrow domain-required service contracts, carry WorkflowExecutionID and IdentityID, and resolve the owning capability binding. Root-client fallback is forbidden.                                                                                                                                                                                                |
| Per-task worktree, branch, volume, and cleanup     | daemon, worktree, storage, container                            | WorkflowExecution workspace                              | Every WorkflowExecution receives isolated workspace state. Workspace lifecycle is explicit, identity-aware, recoverable, and controlled outside the model.                                                                                                                                                                                                                        |
| Task retry, dead, waiting-human, PR reconciliation | daemon, workflow, gateway                                       | WorkflowExecution lifecycle policies                     | Retry, waiting, cancellation, reconciliation, and terminal outcomes are Agent System behaviour. Channels and infrastructure report facts or submit commands; they do not mutate state.                                                                                                                                                                                            |
| Task-run readiness and retry timeouts              | daemon taskrun request loop                                     | Worker-dispatch settings and policy                      | These settings govern WorkflowExecution handoff availability. They are not Workflow data and must be owned by the worker-dispatch capability.                                                                                                                                                                                                                                     |

### Terms that do not migrate

- The `task` command in `Taskfile.yml` is the external Task build tool and keeps
  its name.
- A forge may call its own records issues or tasks inside its channel adapter.
- General prose may use “task” informally only when it clearly refers to an
  external system. Agent System contracts and generated reference MUST use the
  canonical Workflow vocabulary.

## Current WorkflowExecution data inventory

This inventory is the migration ledger for data currently spread across
`store.Task`, database-only columns, `workflow.TaskContext`, worker messages,
and surrounding configuration. Implementation SHOULD NOT require another
repository-wide search to determine what existing execution data must be
preserved or deliberately removed.

| Current data           | Current representation                                      | Required target                                                                                                       |
| ---------------------- | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| Execution identity     | `Task.ID`, `task_id`, NATS subjects, logs                   | Immutable WorkflowExecutionID used consistently by state, events, workers, projections, and capability calls          |
| Owning actor           | `Task.Identity` string                                      | Canonical IdentityID                                                                                                  |
| Source type            | `Task.Source` (`forge` or `chat`)                           | Typed channel and SourceMessageReference; no closed source enum in WorkflowExecution                                  |
| Source address         | `Owner`, `Repo`, `IssueNumber`; synthetic chat issue number | Channel-owned source reference with native message/thread/object identifiers; synthetic identifiers removed           |
| Requested work         | `Title`, `Body`, `Labels`                                   | Immutable execution input captured from the source request; channel-specific metadata retained in the source envelope |
| Workflow selection     | `Workflow` string                                           | Immutable WorkflowID plus selected WorkflowVersion                                                                    |
| Lifecycle              | `Status` string                                             | Enforced WorkflowExecution state                                                                                      |
| Current position       | mutable `Stage` string                                      | Derived current WorkflowStep reference plus durable StepExecution history                                             |
| Attempts               | `Attempt`, `RetryCount`                                     | Execution attempt history and retry-policy evidence; distinguish worker delivery retry from workflow retry            |
| Failure                | `ParkReason`, transition detail, response errors            | Typed failure classification, evidence, retryability, and resulting lifecycle event                                   |
| Human wait             | `WatchCommentID`, `waiting_human`                           | Channel-neutral wait condition and correlation reference; forge comment IDs stay in the forge channel                 |
| Workspace              | `Dir`, `Branch`, worktree path, `task.json`                 | Execution workspace reference and independently managed workspace lifecycle                                           |
| Change outcome         | `BuildSummary`, `BuildNoChanges`, changed files             | Step outputs and final WorkflowExecution outcome                                                                      |
| Reproduction evidence  | `ReproProof`                                                | Typed TDD step evidence retained with the StepExecution and final outcome                                             |
| Planning output        | `Plan`                                                      | Typed step output referenced by later steps rather than a special aggregate field                                     |
| Notes                  | `Notes`, appended agent notes                               | Ordered execution annotations with actor, source step, and timestamp                                                  |
| Pull request           | `PRNumber`, forge host/repository assumptions               | Forge-channel delivery reference and reconciliation state                                                             |
| Agent usage            | `TokensUsed`, `Iterations`                                  | StepExecution usage measurements aggregated into execution projections                                                |
| Decision capture       | private feasibility `decision`, capture-tool output         | Typed WorkflowStep output defined by that Workflow version                                                            |
| Terminal outcome       | mutable `Outcome{Status, Detail}`                           | Explicit WorkflowExecution outcome command/event validated by the state machine                                       |
| Skill context          | `SkillBody`, `SkillPlugins`                                 | Resolved Workflow version dependencies or execution inputs, with provenance                                           |
| Agent prompt context   | `SystemPrompt`, `ExtraRules`, mission strings               | Versioned WorkflowStep definition plus resolved execution context; mutable global prompt callbacks removed            |
| Gates and protection   | `Gate`, `Preflight`, `Protection`, repository config        | Agent System policy definitions resolved for the WorkflowExecution and enforced outside the agent                     |
| Budgets                | steps, tokens, wall clock, gate failures                    | Execution and StepExecution policy values with measured usage and evidence                                            |
| Tool access            | capture tools, registry, guardrails, skill plugins          | Resolved step capability set and policy; global registry pointers do not enter execution state                        |
| Persistence timestamps | SQL `created_at` and `updated_at`                              | Domain timestamps and version used for optimistic concurrency and audit                                               |
| Transition history     | SQL `transitions` and generic events                        | Authoritative WorkflowExecution domain events stored atomically with state                                            |

## Current runtime responsibilities to unify

The following behaviours currently act on the same WorkflowExecution but are
implemented in different packages:

- accept a request from a channel and enforce idempotency;
- select and version a Workflow;
- create, queue, claim, schedule, and execute a WorkflowExecution;
- serialize same-repository execution where policy requires it;
- resolve identity-specific capabilities;
- prepare, retain, recover, and clean up the workspace;
- run deterministic and agent-backed WorkflowSteps;
- enforce gates, budgets, protected paths, tool constraints, and result review;
- persist state and StepExecution outputs;
- publish authoritative lifecycle events;
- wait for channel input and resume;
- classify failure, retry, park, mark exhausted, cancel, or recover after crash;
- deliver and reconcile forge changes;
- project status, history, counts, and audit for channels and the web UI;
- hand execution to a worker without moving domain ownership to the worker.

## Current workflow-feature hazards

- There is no enforced WorkflowExecution state machine. `Store.Transition`
  ignores the supplied `from` state when updating the row.
- State update and transition-history insertion are separate operations, so
  state and audit can diverge.
- Callers frequently discard persistence and transition errors.
- Gateway task control duplicates lifecycle status constants by hand.
- `workflow.TaskContext` is a mutable service bag containing complete
  configuration plus forge, store, worktree, agent, event bus, logging, memory,
  guardrails, skills, and scratch state.
- Lifecycle changes occur in both daemon and workflow code.
- Workflow orchestration runs inside task-scoped agent containers while the
  daemon retains lifecycle, admission, and authority-bearing RPC ownership.
- Forge-backed and chat-created work share one record through source flags and
  synthetic issue numbers.
- Repository labels currently select workflows and mirror task state, coupling
  domain transitions to forge presentation.
- Failure generally becomes `parked`, but failure classification and
  retryability are not represented explicitly.
- Generic observability events are emitted alongside state changes rather than
  being the authoritative domain events.

## Existing invariants to preserve

- The model never performs git operations directly.
- Deterministic policy and gates cannot be waived by the agent.
- TDD captures a failing reproduction before implementation and protects the
  test during the fix.
- Every run ends in an explicit outcome; definition errors and stage errors do
  not disappear silently.
- Crash recovery requeues interrupted work.
- Agent execution crosses a versioned data boundary.
- Forge, persistence, workspace, transport, container, and model runtimes remain
  replaceable infrastructure.
- Work from the same repository is serialized by default.

## Required migration

- Rename the current persisted `store.Task` concept to WorkflowExecution.
- Replace task lifecycle terminology in core commands, events, persistence,
  worker contracts, logs, and generated reference.
- Preserve external source terminology only inside its channel adapter.
- Replace synthetic issue numbers with explicit source-message references.
- Associate every WorkflowExecution with an immutable Workflow identifier and
  version.
- Record StepExecutions explicitly rather than treating one mutable `stage`
  field as complete execution history.
- Keep Attempt as retry state within the same WorkflowExecution.

## Curator isolation invariant

Decided 2026-08-05
([#435](https://github.com/samcharles93/archie-core/issues/435) epic,
[#98](https://github.com/samcharles93/archie-core/issues/98) runtime).
Recovered 2026-08-09 from the pre-migration issue tracker.

**Curators are peers, not dependencies.** They must never block, delay, or take
down chat turns, agent runs, or the daemon.

1. Registry-owned goroutines, one per curator, with per-pass `recover()`,
   per-pass budgets (max steps, tokens, timeout), and a configurable maximum of
   concurrent passes (default 1) so curators cannot saturate the shared LLM
   runtime against chat.
2. Wake events ride the in-process `events.Bus`, which is mutex fan-out with
   bounded per-subscriber buffers that **drop** on overflow
   (`internal/events/events.go`). A slow curator can never backpressure the chat
   publisher.
3. Wake is a best-effort nudge. The trigger is a deterministic read of persisted
   primary-input state at pass time, so a dropped wake only delays a run to the
   next check-in and never corrupts trigger accounting
   ([#22](https://github.com/samcharles93/archie-core/issues/22)).
4. The curator registry lifecycle is independent of the agent loop. Daemon
   shutdown cancels in-flight passes and waits bounded.

Chat turns keep their per-session lanes; agent runs keep their workflow
goroutines and containers. Rule of thumb: **no code path a curator executes may
ever appear on a chat turn's critical path.**
