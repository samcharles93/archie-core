# archie-core Architecture

**archied** is a resident daemon that polls GitHub for issues labelled `archie`, works each one in an isolated worktree through a routed workflow, and opens pull requests for human review.

## Package Map

| Package | Role |
|---|---|
| `cmd/archied/` | Binary entry: config→forge→store→runtime→workflows→daemon |
| `cmd/archie-agent/` | One-invocation autonomous stage worker (JSON stdin/stdout) |
| `internal/config/` | TOML config, per-repo gates, budgets, providers, ecosystems |
| `internal/daemon/` | Resident loop: poll, enqueue, claim, route, process, reconcile |
| `internal/workflow/` | Engine: stage lists, routing, shared steps, workflow definitions |
| `internal/agentexec/` | Versioned agent-stage protocol, worker, and in-process/subprocess runners |
| `internal/forge/` | GitHub interface: issues, labels, PRs, comments, reactions |
| `internal/worktree/` | Git operations: clone, branch, commit, push, diff, cleanup |
| `internal/store/` | SQLite: task rows, status transitions, crash recovery |
| `internal/events/` | In-process event bus: publish/subscribe for observability |
| `internal/webui/` | SSE dashboard: live event stream, task status |
| `ai-sdk/runtime` | External: provider-agnostic LLM runtime |
| `ai-sdk/agentloop` | External: gated agent loop with budgets and tool jails |
| `ai-sdk/core` | External: tool definitions, generate options |

## Task Lifecycle

```
queued → running(workflow:stage) → pr_open → merged | rejected
                 ↓
           waiting_human → (human approves) → queued(implement)
                 ↓
            parked
```

**Crash recovery:** tasks left in `running` are re-queued on startup. Parks are never silent  --  every park posts a comment with the reason and gate output.

**PR reconciliation:** the daemon polls open PRs, checks GitHub state (`merged`/`closed`), transitions the task, and cleans up worktrees.

## Workflow Engine

### Core types (`workflow.go`)

- `Workflow` = named list of `Stage`s
- `Stage` = `Name` + `Run(ctx, *TaskContext) error`
- `TaskContext` = mutable bag: Task, Repo, Config, Forge, Store, Agent runner, worktree dir/branch, scratch fields
- `Outcome` = terminal decision (status + detail)  --  ends the workflow immediately
- `Registry` = `map[string]Workflow`

### Routing (`Route`)

Label-driven, no LLM involved. Pre-assigned workflow wins; then labels decide:
- `bug` → `tdd`
- `feature` → `feasibility`
- `bootstrap` → `bootstrap` (diagnostics, no LLM spend)
- Default → `implement`

### Execution (`Run`)

Stages execute sequentially. After each stage:
- Task is persisted to SQLite
- Error → park the task (comment on issue, keep worktree)
- Outcome set → finish (transition task, update labels)
- Neither → continue to next stage

A workflow that ends without an outcome parks  --  definition bugs must not vanish silently.

## Workflows

### Bootstrap (diagnostics, no LLM)

```
prepare → apply(marker file) → commit-push → diff-cap → open-pr
```

Proves the plumbing: clone, push, PR. Label any test issue `bootstrap` to verify end-to-end.

### Implement (default)

```
prepare → baseline-gate → plan(planner, read-only) → build(builder, gated) → commit-push → diff-cap → open-pr
```

- **baseline-gate:** runs the repo's gate at the base commit before anything is touched
- **plan:** read-only agent explores codebase, produces implementation plan, posts it as a comment
- **build:** full toolset agent implements against the plan behind the repo's quality gate
- **diff-cap:** parks oversized changes (>400 lines default)
- **open-pr:** builder's summary becomes the PR body

### TDD (bug label)

```
prepare → baseline-gate → analyse(planner) → repro-tests(builder, inverted gate) → capture-proof → commit-repro → fix(builder, normal gate, test files protected) → commit-push → diff-cap → open-pr + repro-evidence
```

- **repro-tests:** the quality gate is **inverted**  --  the test command must FAIL. Proven failure is required.
- **capture-proof:** runs the test command deterministically, captures the failing output
- **commit-repro:** commits the failing tests as the first commit
- **fix:** test files are **environmentally write-protected** (not prompt-ruled). The agent fixes code, not tests.
- **open-pr + evidence:** posts both the PR and the captured failure proof

### Feasibility (feature label)

```
prepare → assess(planner, with decide tool) → [close_if_bad_fit | prd(planner)] → deliver → waiting_human
```

- **assess:** reads AGENT.md, ROADMAP.md, README. Calls a `decide` tool (structured output, not parsed prose). Bad fit → close with reasons.
- **prd:** read-only agent writes a PRD: problem, solution, files affected, acceptance criteria, non-goals, diff estimate
- **deliver:** posts PRD as issue comment + webhook notification (n8n → email). Sets state to `waiting_human`.
- The daemon watches for human replies, judges them with a triage LLM (APPROVE/REJECT/UNCLEAR), requeues or closes.

## Forge Interface

A thin abstraction over GitHub (Gitea planned):

| Method | Purpose |
|---|---|
| `AcceptInvitations` | Auto-accept repo invites |
| `AssignedIssues` | Poll assigned issues (excluding PRs) |
| `IssuesWithLabel` | Poll labelled issues |
| `Comment` | Post issue/PR comment, return ID |
| `RepliesAfter` | Find human replies after a comment ID |
| `CreatePR` | Open pull request |
| `PRState` | Check merged/open/closed |
| `CloseIssue` | Close with final comment |
| `React` | Emoji reaction (pickup acknowledgement) |
| `SetStateLabel` | Replace old state label with new one |
| `VerifyPush` | Confirm push permission at startup |

State labels (`archie:queued`, `archie:working`, etc.) are created on demand, removed when transitioning.

## Worktree Manager

Every task gets a fresh clone  --  no persistent state between runs. The model never runs git.

```
~/.local/share/archie/work/<owner>-<repo>/issue-<N>/
```

Token auth via a generated `GIT_ASKPASS` helper script  --  the token never appears in `.git/config` or process argv.

## Config

Daemon-level TOML (`~/.config/archie/config.toml`):

- `[forge]`  --  type (github), host, token secret reference
- `[dispatch]`  --  trigger (assignee/label/either), labels, ack reaction
- `[[repos]]`  --  owner, name, base branch, gate commands, protected paths, ecosystem
- `[models]`  --  planner, builder, triage (provider/model refs)
- `[providers]`  --  LLM provider configs
- `[agent]`  --  execution mode (`inprocess` or `subprocess`), worker command, environment allowlist
- `[budgets]`  --  max steps, max tokens, wall clock, gate max failures
- `[web]`  --  dashboard listen address
- `[notify]`  --  webhook URL for notifications

Secrets (forge token, provider API keys, channel tokens) are not embedded in config files. `internal/secret` defines a pluggable `Engine` interface (`Name`, `Version`, `Resolve(key) (string, error)`) resolved through a `Registry`; the built-in `env` engine reads an environment variable by name. Custom engines (sops, Bitwarden Secrets Manager, Vault/OpenBao) are Yaegi-interpreted `.go` plugins loaded from a directory, following the same convention as `internal/plugin`'s daemon plugins. Config fields reference a secret as `{engine, key}` (`secret.SecretRef`), e.g. `token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }`. As of this writing only `Forge.Token` has been migrated to `SecretRef`; `Provider.APIKeyEnv`, `NATSConfig.TokenEnv`, and `TelegramConfig.TokenEnv` are still plain env-var-name strings pending a follow-up (they're forwarded by name to `ai-sdk/runtime` and subprocess workers rather than resolved centrally, which needs its own design pass before migrating).

Subprocess mode is a migration transport boundary, not a security sandbox: the worker still runs under the daemon UID and can access daemon-readable host resources. The "daemon never runs untrusted code" boundary requires the later container runner with a separate user, a restricted filesystem and process namespace, and explicit mounts.

Per-repo gates are command lists: `[[repos.gate]] = ["go", "vet", "./..."]`. The last command is the test runner (by convention  --  TDD inverts it).

## Observability

Every lifecycle event flows through an internal event bus:
- SQLite event log (every event stamped with a row ID)
- Live dashboard via SSE (server-sent events)
- Bounded subscriber buffers  --  slow consumers drop events, never block the daemon

## Key Design Decisions

1. **Environmental constraints over prompt rules.** The gate, test-file protection, and diff cap are code enforcement  --  not "please don't do that" in the system prompt.
2. **The model never runs git.** Worktree operations are deterministic steps, not agent tools.
3. **Tokens as cost, not features.** Token usage is tracked per task and emitted in observability events but never budgeted beyond the agent loop's own limits.
4. **Label-driven routing.** Workflow selection is a switch statement, not an LLM call. A triage LLM is used only for the human reply judgment in waiting_human.
5. **Shared code with tau at the module level.** `ai-sdk/agentloop` and `ai-sdk/toolkit` are extracted from tau's coordinator and promoted to shared libraries.
6. **No daemon-internal cron.** Polling is a ticker loop, not cron. The feasibility PRD notification uses an external webhook (n8n).
7. **Fresh worktree per task.** No persistent state between runs. Crash recovery just re-queues.
8. **Agent execution is a data boundary.** Autonomous stages receive a versioned request and return a fenced result; SQLite, forge operations, git, workflow sequencing, and outcome handling stay daemon-owned.

## What's Implemented vs Planned

**Implemented:**
- Daemon poll loop, enqueue, claim, process
- Bootstrap, implement, TDD, feasibility workflows
- GitHub forge (issues, PRs, labels, comments, reactions)
- Worktree isolation (clone, branch, commit, push, diff, cleanup)
- SQLite store with transition audit
- Config with ecosystem support (go, python, node, rust)
- Agent stages with gates, budgets, preflight, protected paths
- Observability (event bus, SQLite log, SSE dashboard)
- Crash recovery (re-queue stale running tasks)
- Human reply judgment (triage LLM for waiting_human)

**Planned:**
- Gitea forge implementation
- More ecosystems
- Notification integrations beyond n8n webhook
- Skills (SKILL.md support  --  see `.archie/skills/`)
