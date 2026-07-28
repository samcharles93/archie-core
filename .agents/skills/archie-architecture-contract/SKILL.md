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

Use these labels exactly:

| Label | Meaning | Authority |
| --- | --- | --- |
| **CURRENT** | Behavior present in live code and production composition as of 2026-07-28. | Code, tests, and `cmd/archied/main.go` wiring |
| **APPROVED TARGET** | Direction an implementation must approach, but which may not exist on disk. | `docs/prds/01-project-management.md` and documents whose status says approved |
| **OPEN** | A migration or API decision no implementer may invent locally. | `docs/architecture/migration-decisions.md` and documents marked in progress or under design |

> **Do not implement target prose as though it describes current code.**
> Before treating a target package as available, prove that it exists and is
> used from a production entry point. If the task will create that package,
> classify it as a migration slice and obtain the focused package decision
> first. `internal/domain/` does not exist in the CURRENT tree as of 2026-07-28.

Resolve apparent disagreement in this order:

1. Read live code and production composition for CURRENT behavior.
2. Read `docs/prds/01-project-management.md` for APPROVED TARGET ownership.
3. Read the focused decision document for the selected behavior.
4. Read `docs/architecture/migration-decisions.md` for OPEN work.
5. Stop and use `archie-architecture-planning-campaign` if a required semantic
   decision remains open.

## Route the task

Use this skill to enforce boundaries and place behavior.

Do **not** use it as:

- a design interview or migration campaign; use
  `archie-architecture-planning-campaign`;
- a project vocabulary glossary; use `archie-domain-reference`;
- NellDB mechanics, persistence behavior, or adapter guidance; use
  `archie-nelldb`;
- a complete configuration field/default reference; use
  `archie-config-and-flags`;
- proof that code is production-reachable, non-duplicated, or maintainable; use
  `archie-technical-accountability`;
- a test, review, or release checklist; use `archie-change-control`.

Load the sibling skill as well when the task crosses one of those boundaries.

## Know the CURRENT system

Do not simplify these facts into the target model:

| Concern | CURRENT runtime truth on 2026-07-28 | Evidence |
| --- | --- | --- |
| Composition | `cmd/archied/main.go` performs substantial construction and wiring. Packages are organized mostly by technical mechanism. | `cmd/archied/main.go`; current `internal/*` tree |
| Durable work | `store.Task` combines forge/chat source data, workflow position, output, retry, identity, and PR state. | `internal/store/store.go` |
| Workflow | `internal/workflow` routes a `store.Task` into a `map[string]Workflow`, then mutates a broad `TaskContext` through sequential stages. | `internal/workflow/workflow.go` |
| Agent-stage boundary | A versioned `agentexec.Request`/`Result` carries one autonomous stage; validation correlates task, attempt, and stage. | `internal/agentexec/protocol.go` |
| Container handoff | `internal/taskrun` also carries an entire task plus `config.Repo` and `config.TaskConfig`; `archie-agent` runs routing and sequencing for this path. | `internal/taskrun/taskrun.go`; `cmd/archie-agent/main.go` |
| Git ownership | Daemon-owned worktree operations clone, commit, push, diff, and clean up. The model receives no git ownership. | `internal/worktree/`; `internal/workflow/steps.go`; `ARCHITECTURE.md` |
| Enforcement | Gates, read-only/protected paths, TDD inverted test gates, and diff caps are represented outside prompt prose. | `internal/agentexec/inprocess.go`; `internal/workflow/{agent,tdd,steps}.go` |
| Configuration | `internal/config.Config`, `IdentityConfig`, `Repo`, and task snapshots mix input decoding with runtime concerns. | `internal/config/config.go` |
| Identity | Configured names and bot usernames act as process-local identity keys. `daemon.IdentityRunner` groups per-name clients/config; there is no durable Identity domain. | `internal/daemon/daemon.go`; `internal/store/store.go`; `internal/gateway/session.go` |
| Messaging | `internal/gateway` contains both legacy `Message` flow and a richer `MessageEvent`; channel routing and chat task control are mixed with conversational behavior. | `internal/gateway/{gateway,messageevent,tasks}.go`; `internal/channels/` |
| Plugins | `plugin.Plugin` exposes only `Name` and `Version`; current loading interprets operator-installed Go files with Yaegi. | `internal/plugin/plugin.go` |
| Processes | Agent modes include in-process, subprocess, and NATS. Optional containers add a distinct boundary with known gaps; a subprocess alone is not a security sandbox. | `internal/config/config.go`; `cmd/archied/main.go`; `ARCHITECTURE.md` |

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

Do not call this an enforced state machine. CURRENT
`Store.Transition(ctx, taskID, from, to, detail)`:

- does not include `from` in the update predicate;
- updates task state and inserts transition history in separate statements;
- can therefore accept an illegal/stale transition or leave state and history
  inconsistent;
- is called from paths that sometimes discard its error.

Treat atomic state plus event/history persistence as an OPEN migration design,
not a small local patch.

## Preserve the load-bearing invariants

| Invariant | Required action | Status and source |
| --- | --- | --- |
| The model never runs git | Keep clone, branch, commit, push, diff, and cleanup in deterministic workspace/worktree steps. Never expose git as an agent tool. | CURRENT: `ARCHITECTURE.md`; `internal/worktree/` |
| Deterministic constraints outrank prompts | Enforce gates, protected paths, read-only stages, test protection, and change caps outside model instructions. Do not replace enforcement with “must not” prose. | CURRENT: `ARCHITECTURE.md`; `internal/agentexec/inprocess.go`; `internal/workflow/` |
| Agent execution is a data boundary | Send bounded, versioned input; validate correlated output before applying it. Keep persistence, forge credentials, git authority, workflow outcomes, and policy enforcement outside the autonomous model. | CURRENT foundation: `internal/agentexec/protocol.go`; `ARCHITECTURE.md` |
| Every workflow ends explicitly | Preserve terminal outcome or visible park behavior. Preserve crash recovery of interrupted `running` work. | CURRENT: `internal/workflow/workflow.go`; `internal/store/store.go` |
| Generic plugins stay metadata-only | Keep `plugin.Plugin` at `Name()` and `Version()`. Put behavior on a typed capability interface with an owning `Registry` or `Manager`. | CURRENT-enforced rule: `internal/plugin/architecture_test.go`; `ARCHITECTURE.md#plugin-engine-rule-strict` |
| Capability engines own lifecycle | Give resource-owning engines explicit start/health/stop semantics and failure isolation. Supply narrow registrars/services, never `*daemon.Daemon` or a service locator. | APPROVED rule: `ARCHITECTURE.md#plugin-engine-rule-strict`; reviewers enforce non-mechanical parts |
| Trust is explicit | Treat trusted operator-installed in-process code, repository code, out-of-process integrations, and container-isolated code as different trust classes. Never call Yaegi or subprocess execution a sandbox. | APPROVED rule: `ARCHITECTURE.md#plugin-engine-rule-strict` |
| Infrastructure stays replaceable | Keep forge, persistence, workspace, transport, container, and model details behind behavior-owned contracts. Do not leak broker messages, DB rows, SDK types, or config DTOs into domain models. | APPROVED TARGET: `docs/architecture/dependencies-and-contracts.md` |
| Same-repository execution serializes by default | Preserve the default unless an explicit scheduling policy permits concurrency. | CURRENT: `internal/config/config.go`; `internal/daemon/daemon.go` |

Run the mechanical plugin guard after changing any plugin or `*Engine`
interface:

```bash
go test ./internal/plugin -count=1
```

Passing it does **not** prove lifecycle, host-access, shutdown, or trust
semantics. Review those manually through `archie-change-control`.

## Apply the APPROVED TARGET

Apply these ownership rules to migration plans and new seams. Do not claim they
are CURRENT until production wiring proves the cutover.

### Keep dependency direction inward

Use this target direction:

```text
domain and shared application contracts <- infrastructure implementations
                    ^
                    |
          application composition
```

Enforce:

1. Put cohesive behavior, vocabulary, state, commands, events, policies,
   runtime settings, and required service interfaces with its owning domain.
2. Make infrastructure implement domain-required interfaces and translate
   external representations.
3. Let only `internal/app` choose concrete implementations and connect domains.
4. Keep `cmd/*` limited to process input and invoking composition.
5. Use an owned command, event, or approved shared contract across domains.
6. Never let one domain mutate another domain's state or call its internals.
7. Reject generic `entities`, `services`, `repositories`, `messages`, or
   `utils` layers that merely group mechanisms.

Sources: `docs/architecture/organisation.md` and
`docs/architecture/dependencies-and-contracts.md`.

### Keep source ownership independent of deployment

Begin with a modular monolith even when capabilities run in separate processes.
Introduce or retain a process/service boundary only for demonstrated:

- security isolation;
- independent scaling;
- failure containment; or
- independent operation.

Do not add a network protocol or duplicate service scaffolding for possible
future extraction. A transport is an infrastructure implementation of a
behavior-owned contract, not the owner of the behavior.

Source: `docs/architecture/organisation.md`.

### Separate interaction, admission, assistance, and execution

Use this target handoff:

```text
channel-neutral Message
  -> selected Agent turn or deterministic automation
  -> optional proposed work
  -> Work Intake validates and emits AcceptedWorkRequest
  -> Workflow domain selects a Workflow version
  -> Workflow domain creates WorkflowExecution
```

Keep the concepts separate:

- **Messaging** owns canonical messages, conversations, branches, channel/source
  correlation, delivery attempts, and messaging projections.
- **Agent** owns a persistent assistant, model-independent continuity,
  specialization, capability context, and memory associations.
- **Work Intake** owns admission, validation, deterministic routing, and the
  accepted-work-request handoff.
- **Workflow** owns reusable versioned definitions, steps, durable executions,
  attempts, outcomes, and typed Workflow plugin contracts.

Do not equate an external issue/message with a WorkflowExecution. Do not let
Work Intake create one directly. Do not require ordinary conversation or direct
tool use to become durable workflow-backed work.

Sources: `docs/architecture/migration-decisions.md`,
`docs/architecture/agent-system.md`, and
`docs/architecture/messaging-and-work-intake.md`.

### Separate identity, Agent, conversation, and memory

Use immutable `IdentityID` for target attribution; never use display names,
forge usernames, bot names, or the empty string as canonical identity. Keep
Identity separate from Agent: Identity names the actor; Agent owns assistant
behavior and continuity.

Preserve the four fixed target memory scopes:

| Scope | Visibility |
| --- | --- |
| Global shared | Intentionally shared across Archie |
| Agent-wide | One Agent across its users; private to that Agent by default |
| User-wide | One user across authorized Agents |
| Agent-user relationship | Exactly one Agent and one user |

Resolve Agent and initiating user before loading memory. Do not use conversation
branching as a fifth memory scope.

Sources: `docs/architecture/identity.md`,
`docs/architecture/agent-system.md`, and
`docs/architecture/migration-decisions.md`.

### Dissolve global runtime configuration

Treat the current `internal/config` package as a migration source inventory, not
a target domain:

- move file/env/overlay/secret decoding to
  `internal/infrastructure/configuration`;
- let each domain or plugin own typed runtime settings for its behavior;
- translate external DTOs to those settings in `internal/app`;
- never pass complete application config into a domain;
- never move `config.Repo` or `IdentityConfig` intact to a renamed shared
  package;
- classify every behavioral value as invariant, runtime setting, policy value,
  derived value, or runtime state.

Do not add another unrelated concern to the global config model without an
explicit compatibility plan and deletion criterion. Use
`archie-config-and-flags` for the field ledger.

Source: `docs/architecture/configuration.md`.

## Place new behavior deliberately

Use this decision table. “Target” entries require a reviewed migration slice
when the target package is not yet live.

| Behavior | Target owner/location | Decision gate |
| --- | --- | --- |
| Persistent actor lifecycle and attribution | `internal/domain/identity` | Do not mix authentication/access policy into Identity. |
| Persistent assistant behavior and continuity | `internal/domain/agent` | Keep model/provider replaceable; do not fold Workflow into Agent. |
| Workflow definitions, steps, executions, attempts, outcomes | `internal/domain/workflow` | Preserve version ownership and one authoritative lifecycle. |
| Canonical messages, conversations, branches, delivery | `internal/domain/messaging` | Keep platform IDs as external correlation, not canonical IDs. |
| Admission and deterministic routing of proposed durable work | `internal/domain/workintake` | Emit an accepted request; do not construct execution state. |
| Generic plugin identity/discovery/compatibility/lifecycle | `internal/domain/plugin` | Keep generic contract metadata-only. |
| Capability-specific plugin behavior | Owning domain's typed registry/manager | Never add behavior to `plugin.Plugin`. |
| Broker-neutral publication/subscription mechanics | `internal/eventbus` | Keep message meaning and schemas with their domain. |
| Shared policy evaluation/evidence mechanics | `internal/policy` | Keep policy vocabulary, evaluators, and consequences with the consuming domain. |
| NATS, SQLite/NellDB, forge, config decoding, external SDK adapters | `internal/infrastructure/<capability>` | Implement a narrow owner-defined contract; translate at boundary. |
| Construction, cross-domain connection, startup/shutdown | `internal/app/archied` or `internal/app/agentworker` | Keep `cmd/*` thin; define health and shutdown order. |
| First-party optional capability | `plugins/<plugin>` | Make it removable; give only narrow contracts/services. |
| Deployment assembly | `deployments/<assembly>` | Do not move application behavior into deployment files. |
| Unreviewed memory/tools/workspace/gate/storage/web/RPC/scheduling behavior | **OPEN** | Run `archie-architecture-planning-campaign`; do not guess placement. |

## Confront known weak points

State these plainly in every affected plan:

| Weak point on 2026-07-28 | Required response |
| --- | --- |
| `TaskContext` is a mutable service bag containing complete config, services, policy, and scratch state. | Do not add another unrelated field casually. Design narrow step inputs/contracts and a staged removal path. |
| Workflow lifecycle is split across store, daemon, workflow, gateway, and worker code. | Trace every writer and reader before changing status. Require one owner, parity tests, and deletion criteria. |
| State changes and transition history are non-atomic; `from` is advisory. | Treat state-machine and atomic-event work as architecture migration. Do not “fix” one caller and claim completion. |
| In-process and container paths put workflow orchestration on different sides of the process boundary. | Decide which path survives and why before extending both. Fence compatibility adapters and name their deletion gate. |
| Identity uses strings; task uniqueness and NATS deduplication omit identity. | Do not promise independent multi-identity ownership until canonical IDs, uniqueness, and transport correlation migrate. |
| Container RPC registration uses root forge/worktree clients, not the task owner's identity bindings. | Treat non-root container execution as a known trust/correctness defect; never describe it as identity-isolated. |
| Missing or malformed container `task.json` makes `archie-agent` join the shared task-run queue. | Do not claim a per-task workspace binding after this fallback. Require valid boot identity to fail closed before treating the path as isolated. |
| Subprocess execution uses the daemon user and host access. | Never describe subprocess mode as a security boundary. Require a real container/isolation design for confidentiality or integrity. |
| Gateway duplicates lifecycle strings and has competing message contracts. | Do not add a third contract or another direct lifecycle mutation path. |
| Global config and `IdentityConfig` combine unrelated owners. | Do not cure complexity with a renamed global structure or an untyped `Extra` map. |
| Memory has fixed scope requirements but OPEN final placement and migration. | Preserve scope isolation; use focused design before moving providers or persistence. |
| Tests can make unused or duplicate code appear legitimate. | Prove production reachability from composition and dispose of the superseded path; use `archie-technical-accountability`. |

## Control every architecture-affecting change

Classify the change before editing:

| Class | Action |
| --- | --- |
| Current-path repair | Stay inside the current owner, preserve invariants, and avoid speculative target scaffolding. |
| Migration slice | Record current owner, target owner, state/contracts to preserve, compatibility adapter, cutover, and objective deletion criteria. |
| New or changed architecture | Stop implementation and run `archie-architecture-planning-campaign` until the focused decision is approved. |

Before implementation:

- [ ] Name the single behavior under change.
- [ ] Trace its production entry point, constructor, consumers, mutable state,
      persistence, lifecycle, config, tests, and process crossings.
- [ ] Search for duplicates, alternate implementations, and older code paths.
- [ ] Read the focused decision document; do not infer authority over neighbors.
- [ ] Mark every claim CURRENT, APPROVED TARGET, or OPEN.
- [ ] Name the authoritative owner and every superseded path.
- [ ] Define parity, cutover, rollback, and deletion evidence.
- [ ] Route unresolved ownership or semantics to the architecture campaign.

Use evidence, not name matching:

```bash
rg -n '<constructor>|<interface>|<method>' cmd internal
go list -deps ./cmd/archied | rg 'github.com/samcharles93/archie-core/internal/'
gopls references path/to/file.go:<line>:<column>
```

Use `gopls references` or another AST-aware reference query when interfaces,
embedding, generated symbols, or same-named methods make text search ambiguous.
Trace from `cmd/archied/main.go`; a unit test, constructor, or exported symbol
does not prove production use.

After implementation:

- [ ] Show the production call path to the changed behavior.
- [ ] Show that state has one authoritative writer or explicitly fence the
      temporary compatibility path.
- [ ] Show what old path was deleted, retained with rationale, or assigned a
      dated/objective deletion gate.
- [ ] Re-run focused tests plus `archie-change-control`.
- [ ] Run an independent architecture review that assumes every new boundary is
      wrong until evidence proves it.
- [ ] Reject “tests pass” as sufficient evidence of maintainability.

## Provenance and maintenance

Volatile facts in this skill were verified on **2026-07-28**. Re-verify them
before architecture work:

```bash
test ! -d internal/domain && echo 'target domain tree is not current'
sed -n '1,90p' docs/prds/01-project-management.md
sed -n '1,260p' docs/architecture/migration-decisions.md
sed -n '1,320p' internal/store/store.go
sed -n '1,240p' internal/workflow/workflow.go
sed -n '1,220p' internal/agentexec/protocol.go
sed -n '620,680p' cmd/archied/main.go
sed -n '930,975p' internal/daemon/daemon.go
go test ./internal/plugin -count=1
command -v gopls && gopls help references
```
