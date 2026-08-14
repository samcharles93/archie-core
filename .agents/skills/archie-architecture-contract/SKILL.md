---
name: archie-architecture-contract
description: >-
  Protect Archie's load-bearing architecture while placing, implementing, or
  reviewing behavior. Load this skill before changing daemon composition,
  domains, workflows, task lifecycle, agent execution, messaging, work intake,
  identity, memory, configuration, plugins, policies, persistence, NATS/RPC,
  containers, or process boundaries; use it when deciding where new code
  belongs, enforcing dependency direction, separating current runtime truth
  from the approved target and open migration decisions, or ensuring every
  behavior has one authoritative path. Route proof of duplication, dead-code
  disposition, production reachability, and maintainability to
  archie-technical-accountability.
---

# Archie Architecture Contract

Treat architecture as executable safety policy. Preserve one understandable
owner and one production path for each behavior.

## Read status before code

| Label | Meaning | Authority |
| --- | --- | --- |
| **CURRENT** | Behavior present in live code and production composition as of 2026-07-28. | Code, tests, and `cmd/archied/main.go` wiring |
| **APPROVED TARGET** | Direction an implementation must approach, but which may not exist on disk. | `docs/prds/01-project-management.md` and documents whose status says approved |
| **OPEN** | A migration or API decision no implementer may invent locally. | `docs/architecture/migration-decisions.md` and documents marked in progress or under design |

> **Do not implement target prose as though it describes current code.**
> Before treating a target package as available, prove that it exists and is
> used from a production entry point. `internal/domain/` does not exist in the
> CURRENT tree as of 2026-07-28.

Resolve apparent disagreement in this order:

1. Read live code and production composition for CURRENT behavior.
2. Read `docs/prds/01-project-management.md` for APPROVED TARGET ownership.
3. Read the focused decision document for the selected behavior.
4. Read `docs/architecture/migration-decisions.md` for OPEN work.
5. Stop and use `archie-architecture-planning-campaign` if a required semantic
   decision remains open.

Route to `archie-domain-reference` for glossary and
`archie-config-and-flags` for config enumeration.

## Know the CURRENT system

Do not simplify these facts into the target model:

| Concern | CURRENT runtime truth on 2026-07-28 | Evidence |
| --- | --- | --- |
| Composition | `cmd/archied/main.go` performs substantial construction and wiring. | `cmd/archied/main.go`; current `internal/*` tree |
| Durable work | `store.Task` combines forge/chat source data, workflow position, output, retry, identity, and PR state. | `internal/store/store.go` |
| Workflow | `internal/workflow` routes a `store.Task` into a `map[string]Workflow`, mutates a broad `TaskContext` through sequential stages. | `internal/workflow/workflow.go` |
| Agent-stage boundary | Versioned `agentexec.Request`/`Result` carries one autonomous stage; validation correlates task, attempt, and stage. | `internal/agentexec/protocol.go` |
| Container handoff | `internal/taskrun` carries entire task plus `config.Repo` and `config.TaskConfig`; `internal/app/agentworker` owns routing and sequencing while its NATS adapter owns wire mechanics. | `internal/taskrun/taskrun.go`; `internal/app/agentworker/`; `internal/infrastructure/agenttransport/nats/` |
| Git ownership | Daemon-owned worktree operations clone, commit, push, diff, and clean up. | `internal/worktree/`; `internal/workflow/steps.go`; `ARCHITECTURE.md` |
| Enforcement | Gates, read-only/protected paths, TDD inverted test gates, and diff caps are represented outside prompt prose. | `internal/agentexec/inprocess.go`; `internal/workflow/{agent,tdd,steps}.go` |
| Configuration | `internal/config.Config`, `IdentityConfig`, `Repo`, and task snapshots mix input decoding with runtime concerns. | `internal/config/config.go` |
| Identity | Configured names and bot usernames act as process-local identity keys. There is no durable Identity domain. | `internal/daemon/daemon.go`; `internal/store/store.go`; `internal/gateway/session.go` |
| Messaging | `internal/gateway` contains both the `Message` struct flow and richer `MessageEvent`; channel routing and task control are mixed with conversational behavior. | `internal/gateway/{gateway,messageevent,tasks}.go`; `internal/channels/` |
| Plugins | `plugin.Plugin` exposes only `Name` and `Version`; current loading interprets operator-installed Go files with Yaegi. | `internal/plugin/plugin.go` |
| Processes | Agent modes include in-process, subprocess, and NATS. Optional containers add a distinct boundary with known gaps. | `internal/config/config.go`; `cmd/archied/main.go`; `ARCHITECTURE.md` |

### Interpret the current lifecycle honestly

Use these CURRENT status values:

```text
queued, running, waiting_human, pr_open, merged,
parked, dead, rejected, closed_wont_do
```

The intended flow includes queue/claim, workflow stages, human wait/resume, PR
reconciliation, parking/retry/exhaustion, and crash recovery. Read
`internal/store/store.go`, `internal/daemon/daemon.go`, and
`internal/workflow/workflow.go` before changing a transition.

Current `Store.Transition(ctx, taskID, from, to, detail)` does not include
`from` in the update predicate; updates task state and inserts transition
history in separate statements; can accept an illegal/stale transition or leave
state and history inconsistent; is called from paths that sometimes discard its
error. Treat atomic state plus event/history persistence as an OPEN migration
design.

## Preserve the load-bearing invariants

| Invariant | Required action | Status and source |
| --- | --- | --- |
| The model never runs git | Keep clone, branch, commit, push, diff, and cleanup in deterministic workspace/worktree steps. | CURRENT: `ARCHITECTURE.md`; `internal/worktree/` |
| Deterministic constraints outrank prompts | Enforce gates, protected paths, read-only stages, test protection, and change caps outside model instructions. | CURRENT: `ARCHITECTURE.md`; `internal/agentexec/inprocess.go`; `internal/workflow/` |
| Agent execution is a data boundary | Send bounded, versioned input; validate correlated output before applying it. | CURRENT foundation: `internal/agentexec/protocol.go`; `ARCHITECTURE.md` |
| Every workflow ends explicitly | Preserve terminal outcome or visible park. Preserve crash recovery of interrupted `running` work. | CURRENT: `internal/workflow/workflow.go`; `internal/store/store.go` |
| Generic plugins stay metadata-only | Keep `plugin.Plugin` at `Name()`/`Version()`. Behavior on typed capability interface with owning `Registry`/`Manager`. | CURRENT-enforced rule: `internal/plugin/architecture_test.go` |
| Capability engines own lifecycle | Give resource-owning engines explicit start/health/stop semantics and failure isolation. | APPROVED rule: `ARCHITECTURE.md#plugin-engine-rule-strict` |
| Trust is explicit | Treat trusted operator-installed in-process code, repository code, out-of-process integrations, and container-isolated code as different trust classes. | APPROVED rule: `ARCHITECTURE.md#plugin-engine-rule-strict` |
| Infrastructure stays replaceable | Keep forge, persistence, workspace, transport, container, and model details behind behavior-owned contracts. | APPROVED TARGET: `docs/architecture/dependencies-and-contracts.md` |
| Same-repository execution serializes by default | Preserve default unless explicit scheduling policy permits concurrency. | CURRENT: `internal/config/config.go`; `internal/daemon/daemon.go` |

Run the mechanical plugin guard after changing any plugin or `*Engine`
interface:

```bash
go test ./internal/plugin -count=1
```

## Apply the APPROVED TARGET

Apply ownership rules to migration plans and new seams. Not CURRENT until
production wiring proves the cutover.

### Keep dependency direction inward

```text
domain and shared application contracts <- infrastructure implementations
                    ^
                    |
          application composition
```

1. Put cohesive behavior, vocabulary, state, commands, events, policies,
   runtime settings, and required service interfaces with its owning domain.
2. Make infrastructure implement domain-required interfaces and translate
   external representations.
3. Let only `internal/app` choose concrete implementations and connect domains.
4. Keep `cmd/*` limited to process input and invoking composition.
5. Use an owned command, event, or approved shared contract across domains.
6. Never let one domain mutate another domain's state or call its internals.

Sources: `docs/architecture/organisation.md`,
`docs/architecture/dependencies-and-contracts.md`.

### Separate interaction, admission, assistance, and execution

```text
channel-neutral Message -> Agent turn -> optional proposed work
-> Work Intake -> AcceptedWorkRequest -> Workflow selects version -> WorkflowExecution
```

- **Messaging** owns canonical messages, conversations, branches, channel correlation, delivery.
- **Agent** owns persistent assistant, continuity, specialization, memory.
- **Work Intake** owns admission, validation, routing, accepted-work-request handoff.
- **Workflow** owns versioned definitions, steps, executions, attempts, outcomes.

Do not equate external issue/message with WorkflowExecution; do not let Work
Intake create one directly.

Sources: `docs/architecture/migration-decisions.md`,
`docs/architecture/agent-system.md`, `docs/architecture/messaging-and-work-intake.md`.

### Separate identity, Agent, conversation, and memory

Use immutable `IdentityID` for target attribution. Keep Identity separate from
Agent. Four fixed memory scopes:

| Scope | Visibility |
| --- | --- |
| Global shared | Shared across Archie |
| Agent-wide | One Agent across users; private by default |
| User-wide | One user across authorized Agents |
| Agent-user relationship | Exactly one Agent and one user |

Do not use conversation branching as a fifth memory scope.

Sources: `docs/architecture/identity.md`, `docs/architecture/agent-system.md`,
`docs/architecture/migration-decisions.md`.

### Dissolve global runtime configuration

Treat `internal/config` as migration source inventory:

- move file/env/overlay/secret decoding to `internal/infrastructure/configuration`;
- let each domain or plugin own typed runtime settings;
- translate external DTOs in `internal/app`;
- never pass complete application config into a domain;
- classify each behavioral value as invariant, runtime setting, policy value,
  derived value, or runtime state.

Source: `docs/architecture/configuration.md`.

## Place new behavior deliberately

| Behavior | Target owner/location | Decision gate |
| --- | --- | --- |
| Persistent actor lifecycle and attribution | `internal/domain/identity` | Do not mix authentication/access policy into Identity. |
| Persistent assistant behavior and continuity | `internal/domain/agent` | Keep model/provider replaceable; do not fold Workflow into Agent. |
| Workflow definitions, steps, executions, attempts, outcomes | `internal/domain/workflow` | Preserve version ownership and one authoritative lifecycle. |
| Canonical messages, conversations, branches, delivery | `internal/domain/messaging` | Keep platform IDs as external correlation. |
| Admission and deterministic routing of proposed durable work | `internal/domain/workintake` | Emit an accepted request; do not construct execution state. |
| Generic plugin identity/discovery/compatibility/lifecycle | `internal/domain/plugin` | Keep generic contract metadata-only. |
| Capability-specific plugin behavior | Owning domain's typed registry/manager | Never add behavior to `plugin.Plugin`. |
| Broker-neutral publication/subscription mechanics | `internal/eventbus` | Keep message meaning and schemas with their domain. |
| Shared policy evaluation/evidence mechanics | `internal/policy` | Keep policy vocabulary, evaluators, and consequences with consuming domain. |
| NATS, SQLite, forge, config decoding, external SDK adapters | `internal/infrastructure/<capability>` | Implement a narrow owner-defined contract; translate at boundary. |
| Construction, cross-domain connection, startup/shutdown | `internal/app/archied` or `internal/app/agentworker` | Keep `cmd/*` thin; define health and shutdown order. |
| Deploy assembly | `deployments/<assembly>` | Do not move application behavior into deployment files. |
| Unreviewed memory/tools/workspace/gate/storage/web/RPC/scheduling | **OPEN** | Run `archie-architecture-planning-campaign`. |

## Confront known weak points

| Weak point on 2026-07-28 | Required response |
| --- | --- |
| `TaskContext` is a mutable service bag with config, services, policy, scratch state. | Design narrow step inputs and staged removal. |
| Workflow lifecycle split across store, daemon, workflow, gateway, worker. | Trace every writer and reader before changing status. |
| State changes and transition history non-atomic; `from` is advisory. | Treat as architecture migration, not local fix. |
| In-process and container paths put orchestration on different sides of process boundary. | Decide which path survives before extending both. |
| Identity uses strings; task uniqueness/NATS dedup omit identity. | Don't promise multi-identity ownership until canonical IDs migrate. |
| Container RPC uses root forge/worktree, not task owner's identity. | Known trust/correctness defect — not identity-isolated. |
| Missing `task.json` makes `archie-agent` join shared queue. | Fail closed before treating as isolated. |
| Subprocess uses daemon user and host access. | Not a security boundary. |
| Gateway duplicates lifecycle strings, competing message contracts. | No third contract or direct lifecycle mutation path. |
| Global config and `IdentityConfig` combine unrelated owners. | Don't rename global structure or add `Extra` map. |
| Memory has fixed scope requirements but OPEN final placement/migration. | Preserve scope isolation; focused design before moving. |
| Tests can make unused/duplicate code appear legitimate. | Prove production reachability from composition. |

## Control every architecture-affecting change

| Class | Action |
| --- | --- |
| Current-path repair | Stay inside current owner, preserve invariants. |
| Migration slice | Record current/target owner, contracts, compatibility adapter, cutover, deletion criteria. |
| New/changed architecture | Stop; run `archie-architecture-planning-campaign` until approved. |

Trace with evidence:

```bash
rg -n '<constructor>|<interface>|<method>' cmd internal
go list -deps ./cmd/archied | rg 'github.com/samcharles93/archie-core/internal/'
gopls references path/to/file.go:<line>:<column>
```

Use `gopls references` or another AST-aware reference query when interfaces,
embedding, generated symbols, or same-named methods make text search ambiguous.
Trace from `cmd/archied/main.go`.
