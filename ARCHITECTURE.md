# archie-core Architecture

**archied** is a resident daemon that discovers forge work — GitHub and Gitea
issues assigned to its bot user, carrying its label, or either (`dispatch`
mode, per identity) — by polling (the default) or, single-identity
deployments only, reacting to forge webhook events the instant they arrive
(`Forge.Intake` = `webhook`/`both`, see `docs/prds/event-sources-and-
reactions.md`), works each matched issue in an isolated worktree through a
routed workflow, and opens pull requests for human review. A single daemon can
serve several identities across several forges, each with its own `bot_user`,
label, and repository set.

## Package Map

This lists the load-bearing packages, not all of them — `internal/` holds ~36.
The domain migration is in progress (`internal/domain/`,
`internal/infrastructure/`), so **the tree is authoritative over this table**.
See `docs/architecture/organisation.md` for the target structure and
`docs/architecture/migration-decisions.md` for the current position.

| Package                      | Role                                                                      |
| ---------------------------- | ------------------------------------------------------------------------- |
| `cmd/archied/`               | Binary entry: config→forge→store→runtime→workflows→daemon                 |
| `cmd/archie-agent/`          | Binary entry: flags, environment, signals, and agentworker invocation     |
| `internal/app/agentworker/`  | Agent worker composition, lifecycle, routing, and workflow execution      |
| `internal/infrastructure/agentboot/` | Worker boot-brief filesystem decoding                                  |
| `internal/infrastructure/agentgit/` | Worker git safe-directory setup                                        |
| `internal/infrastructure/agenttransport/nats/` | Worker core-NATS full-task handoff and RPC client construction                |
| `internal/config/`           | Config types only — loading lives in `infrastructure/configuration`       |
| `internal/daemon/`           | Resident loop: poll, enqueue, claim, route, process, reconcile            |
| `internal/domain/workflow/`  | Engine: stage lists, routing, shared steps, workflow definitions          |
| `internal/domain/workflow/task/` | Task-execution vocabulary (`Task`, statuses, mid-run `Store`, `Definition`) importable without the engine |
| `internal/domain/messaging/` | Canonical messages plus the chat wire contracts (`ChatContract`, sessions, turns, streaming) shared by gateway, channels, webui, and archie-ui |
| `internal/channels/status/`  | Operator-facing channel lifecycle ledger (`State`/`Descriptor`/`Status`/`Manager`); the plugin contract stays in `internal/channels` |
| `internal/domain/workintake/`| `TaskEnvelope`, routing `Kind`, label vocabulary, task subjects           |
| `internal/agentexec/`        | Worker-local stage protocol and ai-sdk loop runner                        |
| `internal/forge/`            | Forge interface: GitHub and Gitea implementations                         |
| `internal/forge/webhook/`    | Optional forge webhook receiver: GitHub issue events → `workintake.TaskEnvelope`, same publish path as polling |
| `internal/webhookguard/`     | Cross-cutting inbound-webhook mechanics: HMAC verification, per-source rate limiting |
| `internal/worktree/`         | Git operations: clone, branch, commit, push, diff, cleanup               |
| `internal/store/`            | Task/capture/mapping/binding SQLite implementation; producer-owned contract interfaces live in `internal/domain/storecontract` (aliases kept here) |
| `internal/domain/storecontract/` | Producer-owned State Store read contracts consumed by webui, intake, and the gRPC adapters (state-store-contract rev. 2d) |
| `internal/events/`           | In-process event bus: publish/subscribe for observability                 |
| `internal/infrastructure/`   | Config loading, eventbus transport, model catalog                        |
| `internal/webui/`            | Shared dashboard HTTP layer served by the archie-ui process |
| `internal/gateway/` (18.7k LOC) | Persistent-connection layer between archie and its users (Telegram, web UI, etc.); shared CommandRouter for dispatch |
| `internal/tools/` (20.9k LOC)   | Central tool subsystem: built-in tool implementations, approval, execution contract |
| `internal/domain/memory/`       | Memory engine contract: four typed scopes, CRUD + revisions, content scanner |
| `internal/infrastructure/memory/` | Builtin memory engine + its `builtin/` markdown store                   |
| `internal/secret/`              | Secret reference resolution across named engines (env, vault, etc.)       |
| `internal/plugin/`              | Core plugin interface: Yaegi-interpreted `.go` plugins loaded from `~/.config/archie/plugins/` and registered with the daemon's extension registry |
| `internal/skill/`               | Parses and discovers agentskills.io `SKILL.md` files                      |
| `ai-sdk/runtime`             | External: provider-agnostic LLM runtime                                   |
| `ai-sdk/agentloop`           | External: gated agent loop with budgets and tool jails                    |
| `ai-sdk/core`                | External: tool definitions, generate options                              |

## Task Lifecycle

```
queued → running(workflow:stage) → pr_open → merged | rejected
                 ↓
           waiting_human → (human approves) → queued(implement)
                 ↓
            parked
```

**Crash recovery:** tasks left in `running` are re-queued on startup. Parks are
never silent, but they no longer comment on the forge issue: the reason is
recorded on the task as `park_reason` (cleared on requeue) and surfaced by the
dashboard and `/api/tasks`. Retries are an explicit operator action capped by
`max_retries`, not a label a human removes.

**Adversarial review:** the implement workflow runs a fresh adversarial review
after the deterministic gates and before `pr_open` (see
`docs/architecture/adversarial-review.md`). A surviving blocking finding — or a
review that failed to run — parks the task with the findings as its
`park_reason`; otherwise the PR opens with a findings section on its body.
Opt-in per repo via `repo.review_enabled`.

**PR reconciliation:** the daemon polls open PRs, checks GitHub state
(`merged`/`closed`), transitions the task, and cleans up worktrees.

**Provenance by origin:** a task's origin determines which enqueue path
created it, though every origin converges on the same `tasks` table and the
same `Route`/`Run` lifecycle above once queued. Forge-sourced tasks — whether
discovered by polling or, single-identity only, delivered by the forge
webhook receiver (`internal/forge/webhook`) — enqueue via the same
`workintake.TaskEnvelope` and `PublishTask` path (see `docs/prds/event-sources-
and-reactions.md`'s decided contract); `TaskEnvelope.IdempotencyKey` is keyed
on `owner/repo/number`, not delivery source, so a webhook-delivered issue and
a later poll of the same issue dedup for free instead of double-enqueuing.
Playbook-binding-dispatched tasks enqueue
via `EnqueueChatTask`/`EnqueueBindingTask` (`internal/store`), the same direct
path chat-spawned tasks use, stamped with `binding_id`/`binding_version` for
provenance. See `docs/architecture/bindings.md` for the binding model and its
threat model in full.

## Workflow Engine

### Core types (`workflow.go`)

- `Workflow` = named list of `Stage`s
- `Stage` = `Name` + `Run(ctx, *TaskContext) error`
- `TaskContext` = mutable bag: Task, Repo, Config, Forge, Store, Agent runner,
  worktree dir/branch, scratch fields
- `Outcome` = terminal decision (status + detail) -- ends the workflow
  immediately
- `Registry` = `map[string]Workflow`

### Routing (`Route`)

Label-driven, no LLM involved. Pre-assigned workflow wins; then labels decide.
The built-in bindings are:

- `bug` → `tdd`
- `feature` → `feasibility`
- `bootstrap` → `bootstrap` (diagnostics, no LLM spend)
- Default → `implement`

Both the closed `bug`/`feature`/`bootstrap` set's target workflow and
arbitrary additional labels are YAML-overridable at daemon startup
(`internal/domain/workflow/routing.go`'s `LoadKindWorkflowsYAML`/
`LoadLabelWorkflowsYAML`, `WorkflowRoutingFile`/`WorkflowLabelsFile`/
`PlaybookDirs` config) -- see `docs/prds/eda-playbook-engine.md`. A label
already owned by the closed kind set, or a binding declared twice across
sources, is a reported load failure, not a silent override.

### Execution (`Run`)

Stages execute sequentially. After each stage:

- Task is persisted to SQLite
- Error → park the task (comment on issue, keep worktree)
- Outcome set → finish (transition task, update labels)
- Neither → continue to next stage

A workflow that ends without an outcome parks -- definition bugs must not vanish
silently.

## Workflows

### Bootstrap (diagnostics, no LLM)

```
prepare → apply(marker file) → commit-push → diff-cap → open-pr
```

Proves the plumbing: clone, push, PR. Label any test issue `bootstrap` to verify
end-to-end.

### Implement (default)

```
prepare → baseline-gate → plan(planner, read-only) → build(builder, gated) → commit-push → diff-cap → open-pr
```

- **baseline-gate:** runs the repo's gate at the base commit before anything is
  touched
- **plan:** read-only agent explores codebase, produces implementation plan,
  posts it as a comment
- **build:** full toolset agent implements against the plan behind the repo's
  quality gate
- **diff-cap:** parks oversized changes (>400 lines default)
- **open-pr:** builder's summary becomes the PR body

### TDD (bug label)

```
prepare → baseline-gate → analyse(planner) → repro-tests(builder, inverted gate) → capture-proof → commit-repro → fix(builder, normal gate, test files protected) → commit-push → diff-cap → open-pr + repro-evidence
```

- **repro-tests:** the quality gate is **inverted** -- the test command must
  FAIL. Proven failure is required.
- **capture-proof:** runs the test command deterministically, captures the
  failing output
- **commit-repro:** commits the failing tests as the first commit
- **fix:** test files are **environmentally write-protected** (not
  prompt-ruled). The agent fixes code, not tests.
- **open-pr + evidence:** posts both the PR and the captured failure proof

### Feasibility (feature label)

```
prepare → assess(planner, with decide tool) → [close_if_bad_fit | prd(planner)] → deliver → waiting_human
```

- **assess:** reads AGENT.md, ROADMAP.md, README. Calls a `decide` tool
  (structured output, not parsed prose). Bad fit → close with reasons.
- **prd:** read-only agent writes a PRD: problem, solution, files affected,
  acceptance criteria, non-goals, diff estimate
- **deliver:** posts PRD as issue comment + webhook notification (n8n → email).
  Sets state to `waiting_human`.
- The daemon watches for human replies, judges them with a triage LLM
  (APPROVE/REJECT/UNCLEAR), requeues or closes.

## Forge Interface

A thin abstraction over the forge. GitHub (`github.go`) and Gitea (`gitea.go`)
both implement it, plus a no-op (`noop.go`) used when no credential resolves —
see the degradation rule in `docs/architecture/configuration.md`. A multi-forge
deployment is configured per identity; see
`deployments/multi-forge-github-gitea.toml`.

| Method              | Purpose                                 |
| ------------------- | --------------------------------------- |
| `AcceptInvitations` | Auto-accept repo invites                |
| `AssignedIssues`    | Poll assigned issues (excluding PRs)    |
| `IssuesWithLabel`   | Poll labelled issues                    |
| `Comment`           | Post issue/PR comment, return ID        |
| `RepliesAfter`      | Find human replies after a comment ID   |
| `CreatePR`          | Open pull request                       |
| `PRState`           | Check merged/open/closed                |
| `CloseIssue`        | Close with final comment                |
| `React`             | Emoji reaction (pickup acknowledgement) |
| `SetStateLabel`     | Replace old state label with new one    |
| `VerifyPush`        | Confirm push permission at startup      |

State labels (`archie:queued`, `archie:working`, etc.) are created on demand,
removed when transitioning. They report state; they do not drive it. Removing
a label no longer retries a parked task -- retries are an explicit operator
action from the dashboard or chat, capped by `max_retries`.

### Webhook intake (alternative to polling)

`internal/forge/webhook` is a separate HTTP receiver, not a `Forge` method: it
verifies a GitHub webhook's HMAC signature (`internal/webhookguard.VerifyHMAC`),
decodes an `issues` event, applies the same dispatch predicate the poller
uses, and calls the same `PublishTask` the poller calls. It is opt-in via
`Forge.Intake` (`poll` default, `webhook`, `both`) and single-identity only --
a deployment with `[[identities]]` configured keeps polling per identity
instead. See `docs/prds/event-sources-and-reactions.md` for why this stayed a
thin decode-and-reuse layer rather than a second intake mechanism.

## Worktree Manager

Every task gets a fresh clone -- no persistent state between runs. The model
never runs git.

```
~/.local/share/archie/work/<owner>-<repo>/issue-<N>/
```

Token auth via a generated `GIT_ASKPASS` helper script -- the token never
appears in `.git/config` or process argv.

## Config

Daemon-level TOML (`~/.config/archie/config.toml`):

- `[forge]` -- type (github/gitea), host, token secret reference; `intake`
  (`poll` default, `webhook`, or `both`) with `webhook_secret`/`webhook_addr`
  when reacting to forge events instead of, or alongside, polling
  (single-identity deployments only)
- `[dispatch]` -- trigger (assignee/label/either), labels, ack reaction
- `[[repos]]` -- owner, name, base branch, gate commands, protected paths,
  ecosystem
- `[models]` -- planner, builder, triage (provider/model refs)
- `[providers]` -- LLM provider configs
- `[nats]` -- embedded or external broker deployment
- `[containers]` -- managed `archie-agent` image, limits, storage, and network
- `[budgets]` -- max steps, max tokens, wall clock, gate max failures
- `[web]` -- dashboard listen address, bound by the standalone archie-ui
  process (the daemon runs no dashboard listener after the UI cutover)
- `[notify]` -- webhook URL for notifications

Secrets (forge token, provider API keys, channel tokens) are not embedded in
config files. `internal/secret` defines a pluggable `Engine` interface (`Name`,
`Version`, `Resolve(key) (string, error)`) resolved through a `Registry`; the
built-in `env` engine reads an environment variable by name and the built-in
`bws` engine caches the accessible Bitwarden project at startup. Custom engines
(sops, Vault/OpenBao) are Yaegi-interpreted `.go` plugins loaded from a
directory, following the same convention as `internal/plugin`'s daemon plugins.
Config fields reference a secret as
`{engine, key}` (`secret.SecretRef`), e.g.
`token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }`. As of this writing
`Forge.Token` and `Provider.APIKey` use `SecretRef`; resolved provider values
are exported under private, identity-scoped environment names because the SDK
and container boundary consume credential names rather than
plaintext wire fields. The older `Provider.APIKeyEnv`, `NATSConfig.TokenEnv`, and
`TelegramConfig.TokenEnv` field names remain supported.

Per-repo gates are command lists: `[[repos.gate]] = ["go", "vet", "./..."]`. The
last command is the test runner (by convention -- TDD inverts it).

## Observability

Every lifecycle event flows through an internal event bus:

- SQLite event log (every event stamped with a row ID)
- Live dashboard via SSE (server-sent events)
- Bounded subscriber buffers -- slow consumers drop events, never block the
  daemon

## Plugin engine rule (strict)

“Engine” is reserved for a typed, lifecycle-managed capability family. It is not
a synonym for a generic plugin hook. Every new plugin engine must satisfy all of
these invariants:

1. **Typed domain contract.** The engine interface exposes real
   capability-specific operations in addition to identity metadata. Examples are
   resolving a secret, delivering a message, or running an agent. A plugin that
   only exposes `Name` and `Version` is metadata, not an engine.
2. **Owning family manager.** The capability package owns a `Registry` or
   `Manager` that controls provider registration and discovery, initialization,
   or access. Providers do not register arbitrary callbacks directly on the
   daemon.
3. **Lifecycle and health.** When an implementation owns resources or background
   work, its typed family contract must expose start, health, and stop
   semantics. The owning manager invokes them and defines failure isolation and
   shutdown ordering. Stateless providers may make these operations explicit
   no-ops.
4. **Narrow host access.** Plugins receive the typed family registrar and only
   the host services their declared capability requires. They never receive
   `*daemon.Daemon`, a service locator, or an untyped hook map.
5. **Metadata-only generic plugin contract.** `plugin.Plugin` remains limited to
   `Name()` and `Version()`. Domain hooks belong on typed engine interfaces;
   adding capability methods to the generic interface is forbidden.
6. **Trust boundary stays explicit.** Explicitly trusted, operator-installed
   plugins—including third-party secret-engine plugins the operator chooses to
   trust—may run in-process and have daemon privileges. Repository-supplied code
   runs in the task container. Integrations that are not trusted as daemon code
   run out of process through MCP or a versioned protocol and, when a
   confidentiality or integrity boundary is required, inside a real container
   sandbox. A subprocess boundary alone is not a security sandbox, and calling
   Yaegi-loaded code a plugin does not isolate it.

`internal/plugin/architecture_test.go` enforces the mechanically testable parts
of this rule: the generic plugin method set, the shared agent-instruction
source, and the requirement that engine interfaces have domain behavior plus an
owning registry or manager.

### Memory engine family

One engine, addressed by four typed scopes -- `ScopeGlobal`, `ScopeAgent`,
`ScopeUser`, `ScopeAgentUser` (`internal/domain/memory`, `Scope`/`ScopeKind`).
A caller names exactly the scopes it may read or write; the engine applies no
access policy of its own, so isolation falls out mechanically because
`ScopeAgent{a}` and `ScopeAgent{b}` are different storage keys. See
`docs/prds/memory-engine-unification.md` for the decision this contract
implements; it superseded `internal/memory` (`MemoryProvider`/`Manager`),
deleted in that PRD's slice 5.

`MemoryEngine` (`internal/domain/memory/contract.go`) is the typed family
contract: `Create`, `Get`, `Query`, `List`, `Update`, `Forget`, `Revisions` --
CRUD with revisions retained, not just create/read/delete. `Update` supersedes
rather than overwrites: the superseded state is appended to the scope's
`HISTORY.md` before the live block is replaced, and `Update` carries no
`Scope` field, so a record can never change scope. The family's content
scanner (prompt-injection / sensitive-data patterns) is applied inside
`Create` and `Update`, the single choke point every producer crosses.

`internal/domain/memory.Registry` is the owning family registry: registration,
start/health/stop, shutdown ordering, and failure isolation, following the
plugin engine rule (`ARCHITECTURE.md#plugin-engine-rule-strict`).
`internal/infrastructure/memory.NewBuiltinEngine` is the only registered
engine today; it persists through `internal/infrastructure/memory/builtin`,
a markdown store (`MEMORY.md`-shaped per-scope files) that owns its on-disk
format end-to-end.

Two per-turn seams in `internal/gateway` connect a chat turn to the engine,
each narrowed to exactly the operations it needs:

- **Read** (`turn_memory.go`, `MemoryStore`): `prepareTurn` runs a
  scope-only `Query` over the resolved `Subject`'s readable scopes,
  synchronously and before the prompt is built. It never returns an error --
  a panicking or failing engine degrades to an empty `<memory>` block rather
  than failing the turn -- and is bounded by a per-scope record limit and a
  byte cap on the rendered block.
- **Write** (`turn_memory_tool.go`, `MemoryWriteStore`): four tools --
  `memory_create`, `memory_update`, `memory_delete`, `memory_list` -- built
  per turn onto the resolved `Subject`'s writable scopes, not registered once
  at boot. The model chooses the scope kind; it never chooses the agent or
  user id, which come from the turn's resolved `Subject` and have no schema
  field to override. `ScopeGlobal` is never model-writable.

`boot.setupMemoryEngine` (`internal/app/archied/bootstrap.go`) registers and
starts the engine and must run before `setupGateways`/`setupGatewayChat`:
those construct every chat turn runner, which captures the engine at
construction time (`internal/app/archied/composition_order_test.go` pins the
ordering). The session curator (`internal/infrastructure/sessioncurator`)
writes derived records onto `ScopeAgentUser`, making it the engine's other
live producer besides the chat write path.

## Key Design Decisions

1. **Environmental constraints over prompt rules.** The gate, test-file
   protection, and diff cap are code enforcement -- not "please don't do that"
   in the system prompt.
2. **The model never runs git.** Worktree operations are deterministic steps,
   not agent tools.
3. **Tokens as cost, not features.** Token usage is tracked per task and emitted
   in observability events but never budgeted beyond the agent loop's own
   limits.
4. **Label-driven routing.** Workflow selection is a switch statement, not an
   LLM call. A triage LLM is used only for the human reply judgment in
   waiting_human.
5. **Shared code.** `ai-sdk/agentloop` and `ai-sdk/toolkit` provide the common
   agent loop and tool set.
6. **No daemon-internal cron.** Polling is a ticker loop, not cron. The
   feasibility PRD notification uses an external webhook (n8n).
7. **Fresh worktree per task.** No persistent state between runs. Crash recovery
   just re-queues.
8. **Agent execution is a data boundary.** Autonomous stages receive a versioned
   request and return a fenced result; SQLite, forge operations, git, workflow
   sequencing, and outcome handling stay daemon-owned.

## What's Implemented vs Planned

**Implemented:**

- Daemon poll loop, enqueue, claim, process
- Forge webhook intake as a feature-flagged alternative/backstop pairing to
  polling (single-identity deployments; see `internal/forge/webhook`)
- Bootstrap, implement, TDD, feasibility workflows
- GitHub and Gitea forges (issues, PRs, labels, comments, reactions)
- Worktree isolation (clone, branch, commit, push, diff, cleanup)
- SQLite store with transition audit
- Config with ecosystem support (go, python, node, rust)
- Agent stages with gates, budgets, preflight, protected paths
- Observability (event bus, SQLite log, SSE dashboard)
- Crash recovery (re-queue stale running tasks)
- Human reply judgment (triage LLM for waiting_human)

**Planned:**

- Multi-identity webhook intake (currently single-identity only)
- More ecosystems
- Notification integrations beyond n8n webhook
- Skills (SKILL.md support -- see `.archie/skills/`)
