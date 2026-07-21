# archie-core v2 Architecture — Design Document

**Author:** Archie (Hermes agent)  
**Date:** 2026-07-21 (implementation status updated 2026-07-22)  
**Status:** Draft — interview-complete, pending final review

---

## 0. Implementation status (as of 2026-07-22)

This document describes a target end state. A parallel exploration of the codebase found that most of it is greenfield — not a delta from current code. The table below is the ground truth; read it before treating any section as already built.

| Area | Status | What actually exists today |
|---|---|---|
| Daemon/agent binary split | **Partial** | `cmd/archied` and `cmd/archie-agent` both exist, but `archied` owns task discovery, routing, and full multi-stage workflow sequencing. `archie-agent` is a 28-line stdin/stdout worker invoked once **per stage**, not a persistent per-task process. See §1. |
| Sandbox / "daemon never runs untrusted code" | **Not true today** | Default `[agent] mode = "inprocess"` runs the LLM agent loop, and therefore all tool calls, inside `archied` itself. `subprocess` mode exists but its own code comment says explicitly it is "not an OS security boundary." No Docker sandbox exists. See §1, §4. |
| NATS / JetStream messaging | **Aspirational — zero code** | No `nats`/`jetstream` dependency, no subject hierarchy, no pub/sub anywhere. Task distribution today is GitHub polling → SQLite `tasks` table used as a queue (`ClaimNext` claims the oldest row) → fully sequential, single-goroutine, in-process execution. Results flow through an in-process `internal/events.Bus` (Go channels) feeding SQLite + SSE. See §2. |
| Docker container sandbox | **Aspirational — zero code** | No Docker/container code anywhere (grep confirms zero implementation hits; the only "container" matches are comments contrasting today's subprocess approach with a hypothetical future runner). `config.example.toml` itself says: *"The worker still runs as the daemon user until the Docker sandbox phase lands."* See §4. |
| `/data/` volume, `task.json` | **Aspirational — zero code** | No file named `task.json` exists anywhere. The closest analog, `store.Task`, is a SQLite-row Go struct with a materially different shape (flat comma-separated `labels`, no nested `config`/`nats` objects) passed by function call, never serialized to a volume. See §3. |
| `[containers]` config block | **Aspirational — zero code** | `internal/config/config.go` has no `[containers]`, `[containers.image]`, or `[containers.persistent_storage]` sections, and `Repo` has no `persistent_storage` override field. See §4. |
| Yaegi extensibility (gates, custom stages, skill scripts) | **Implemented** — but scoped differently than §5 describes | Three surfaces are real, working, and production-tested today: `.archie/gate.go` (custom gates), `.archie/stages/*.go` (custom workflow stages, auto-discovered and run in filename order — no `config.toml`-driven named routing), and skill-local `scripts/*.go` via a `run_go_script` agent tool. All run in-process inside `archied`, re-read from disk every call (no caching layer needed). Full detail lives in the sibling PRD `docs/prds/yaegi-plugin-system.md`, whose Phases 1–3 are marked implemented and match disk state 1:1. See §5, and the reconciliation note there. |
| Layer 1: skill-bundled plugins (`metadata.archie.plugins`, `/data/plugins/`) | **Aspirational — zero code** | No skill currently ships a `plugins/` directory, no code reads `metadata.archie.plugins`, no daemon-side staging to `/data/plugins/`, no volume-precedence conflict resolution. See §5. |
| Layer 2: core daemon plugins (`~/.config/archie/plugins/`, `Plugin` interface) | **Aspirational — zero code** | No `Plugin` interface, no `Register(daemon *Daemon)`, no plugin registry, no loader for `~/.config/archie/plugins/`. Grepped for `Plugin`/plugin registry/`Register(daemon` — zero matches anywhere in the repo. See §5. |
| Skills (`archie-wf-*`, agentskills.io spec) | **Cosmetic only** | Five `SKILL.md` files exist (`ecosystem-node`, `ecosystem-python`, `go-quality-gate`, `security-audit`, `tdd-bugfix`) with a `metadata.archie` block (`tools`, `engine` — no `plugins` or `budget_usd` keys). None use the `archie-wf-*` naming convention. **No Go code parses SKILL.md frontmatter, loads it, or does progressive disclosure at all** — they are documentation of existing hardcoded Go workflow stages, not a loaded specification. One drift found: `tdd-bugfix/SKILL.md` names the final stage "deliver"; the code calls it `open-pr`. See §5. |
| Worktree/repo management | **Implemented, but ephemeral, not volume-based** | `internal/worktree.Manager` does a full fresh `git clone` per task attempt (removing any prior directory first) — no shared object cache, no persistence, no TTL. This answers PRD open question 3: today it is 100% per-task, daemon-owned, non-reusable. See §3, §9. |
| Config schema | **Simpler than §4 describes** | Current `[agent]` block is just `mode` (`inprocess`/`subprocess`) + `command` + `env` — no image registry, no volume TTL, no concurrency cap. See §4. |

**Net read:** the two things that are genuinely further along than a plain reading of this doc's phase list would suggest are (a) the Yaegi extension surfaces (real, tested, in production — just narrower and differently-shaped than §5's Layer 1/2 split), and (b) the `agentexec.Runner` interface (`InProcessRunner`/`SubprocessRunner`), which its own doc comments describe as deliberately designed so a future container runner is a drop-in swap rather than a rewrite. Everything else in this document — NATS, Docker, `/data/` volumes, core plugin registry, agentskills.io loading — is a from-scratch build, not a refactor.

---

## 1. Binary split

### archied (daemon)

The resident orchestrator. Owns:

- **Task discovery** — polls configured forges for issues labelled `archie` (or configured label)
- **NATS publishing** — publishes discovered tasks to JetStream subjects
- **Container lifecycle** — spawns Docker containers, kills them after grace period, cleans up volumes
- **Message gatekeeping** — reads agent responses from NATS, reviews them, forwards to human channels
- **Core plugin loading** — loads Yaegi `.go` plugins from `~/.config/archie/plugins/` at startup
- **Web dashboard** — SSE observability endpoint
- **Human communication** — issue comments, labels, PR management, notify webhook

The daemon never runs untrusted code. Agent containers are the sandbox boundary.

> **Target state, not current state.** Today, default `[agent] mode = "inprocess"` runs the agent loop (and therefore all LLM-directed tool calls) directly inside `archied`. `subprocess` mode exists (`internal/agentexec/subprocess.go`) but its own code comment says: *"This is not an OS security boundary: a same-UID subprocess may still access daemon-readable host resources."* This claim becomes true only once the Docker sandbox (§4, Phase 2) lands.

### archie-agent (ephemeral container)

Spawned per task. Lives in a Docker container. Receives its brief via `/data/task.json` (volume-mounted). Owns:

- **Workflow execution** — runs the routed workflow stages (implement, tdd, feasibility, bootstrap)
- **LLM orchestration** — via `ai-sdk/agentloop`, the agent drives LLM calls
- **Bundled plugin loading** — loads Yaegi `.go` plugins from the skill's `plugins/` directory and the `/data/plugins/` volume
- **Skill loading** — reads `.agents/skills/archie-wf-*` from the worktree, follows the agentskills.io progressive disclosure model
- **NATS reporting** — publishes results to `archie.agent.<task_id>.response`, system messages to `archie.agent.<task_id>.system`

Lifecycle: spawn → load task → run workflow → report → wait for follow-ups (grace period) → killed by daemon.

> **Target state, not current state.** `cmd/archie-agent/main.go` today is 28 lines: read one JSON `Invocation` from stdin, run one stage via `agentexec.ServeOne`, write one `Response` to stdout, exit. It is a single-stage subprocess worker invoked by `archied`'s `SubprocessRunner`, not a persistent per-task container process. Workflow routing and multi-stage sequencing (`workflow.Route`, `workflow.Run`) run entirely inside `archied` (`internal/daemon/daemon.go:process()`), not inside archie-agent. There is no `/data/task.json`, no bundled-plugin loading, no `.agents/skills/archie-wf-*` discovery, and no NATS reporting — see §0, §2, §3, §5. The existing `agentexec.Runner` interface (`InProcessRunner`/`SubprocessRunner`) is, however, deliberately shaped so a future container-backed runner can be a drop-in swap without changing workflow orchestration.

---

## 2. NATS subject hierarchy

> **Status: aspirational, zero code.** No NATS/JetStream dependency exists anywhere in `go.mod` or the codebase. Today, task discovery is GitHub-poll-driven (`daemon.Run()` ticks on `PollInterval`, calls `pollIssues()`), distribution is a SQLite `tasks` table used as a FIFO queue (`store.ClaimNext` atomically claims the oldest `queued` row), and results/status flow through an in-process `internal/events.Bus` (Go channels, multi-subscriber fan-out, drop-on-overflow) that already feeds both a SQLite `events` table and the SSE dashboard. `events.Bus` has roughly the right shape to become the NATS publish path — same `Event` schema, swap the transport — which meaningfully de-risks this phase, but it is currently single-process only with no network transport.

### Task distribution

```
archie.task.bug          — TDD workflow tasks
archie.task.feature      — Feasibility workflow tasks
archie.task.default      — Implement workflow tasks
archie.task.bootstrap    — Bootstrap diagnostic tasks
```

Daemon publishes to the appropriate subject based on label routing. Agents subscribe to subjects they're configured to handle.

### Agent communication (per-task)

```
archie.agent.<task_id>.response    — Agent output: summaries, PR bodies, results. Daemon reviews before human delivery.
archie.agent.<task_id>.system      — Internal comms: log dumps, data requests, health, PII warnings. Never forwarded unprompted.
archie.agent.<task_id>.request     — Optional: agent requests additional data from the daemon.
```

The daemon gatekeeps the response channel — no agent output reaches a human without daemon review.

### JetStream durability

Tasks are published with JetStream persistence. On agent container failure, NATS redelivers. The daemon doesn't need retry logic — NATS handles it.

---

## 3. Persistent volume

> **Status: aspirational, zero code.** No `/data/` directory convention, no `task.json` file, no `session.jsonl`/`memory.jsonl` exist on disk anywhere. The closest thing today is `store.Task` (a SQLite row, passed by function call, different shape from the JSON below — e.g. `Labels` is a comma-separated string, not an array, and there's no nested `config`/`nats` object) plus the `agentexec` wire protocol, which passes an `Invocation` directly over the subprocess's stdin as JSON rather than through a mounted volume. Worktrees are real but ephemeral: `internal/worktree.Manager.Prepare` does a fresh `git clone` per task attempt into `WorkDir/<owner>-<repo>/issue-<N>`, removing any prior directory first — no shared clone cache, no persistence across attempts, no TTL-based cleanup (cleanup today is purely lifecycle-status-driven: removed on terminal merged/rejected outcomes, kept indefinitely while parked).

### /data/ layout

```
/data/
├── task.json              # Full task payload (owner, repo, issue, title, body, labels, workflow)
├── session.jsonl          # Agent session transcript (appended per stage)
├── memory.jsonl           # Optional: cross-session memory (persisted per project)
├── plugins/               # Bundled plugins from the skill (daemon-staged, overrides worktree)
└── worktree/              # Read-only mount of the cloned repo (managed by daemon)
```

### task.json

```json
{
  "id": 42,
  "owner": "sam",
  "repo": "tau",
  "issue_number": 170,
  "title": "Add /copy command for messages and session transcripts",
  "body": "...",
  "labels": ["enhancement", "archie"],
  "workflow": "implement",
  "branch": "archie/issue-170",
  "plan": "",
  "config": {
    "base_branch": "main",
    "gate": [["gofumpt", "-w", "."], ["go", "vet", "./..."], ["go", "test", "-race", "./..."]],
    "protect": ["_templ.go"],
    "ecosystem": "go",
    "test_glob": "*_test.go"
  },
  "nats": {
    "url": "nats://archie-core:4222",
    "response_subject": "archie.agent.42.response",
    "system_subject": "archie.agent.42.system",
    "request_subject": "archie.agent.42.request"
  }
}
```

The daemon writes `task.json` to the volume before the container starts. The agent reads it on boot.

### Volume lifecycle

- Created on task start (persistent per-repo if configured)
- Kept until terminal outcome (`pr_open`, `merged`, `rejected`, `closed_wont_do`) OR TTL expiry
- Parked tasks keep their volume for inspection and requeue reuse
- Cleanup is a daemon responsibility

---

## 4. Container management

> **Status: aspirational, zero code.** `internal/config/config.go` has no `[containers]`, `[containers.image]`, or `[containers.persistent_storage]` sections, and `Repo` has no per-repo `persistent_storage` override. The real config today is a much smaller `[agent]` block: `mode` (`"inprocess"` default, or `"subprocess"`), `command` (defaults to the PATH-resolved `archie-agent` binary name, not a container image), and `env`. There is no Docker/container library dependency in `go.mod`, no `Dockerfile.agent`, and no spawn/kill/grace-period lifecycle code — `SubprocessRunner` uses plain `os/exec.CommandContext` with an env-allowlist, running as the daemon's own OS user. `config.example.toml` already documents this gap directly: *"The worker still runs as the daemon user until the Docker sandbox phase lands."*

### Daemon config

```toml
# Container lifecycle
[containers]
max_concurrency = 4            # max simultaneous agent containers
max_uptime = "30m"             # grace period after task completion before kill
volume_ttl = "72h"             # max volume age before cleanup (overrides terminal wait)

# Image strategy
[containers.image]
registry = "ghcr.io/sam/archie-agent:latest"    # primary: pull from registry
registry_pull = true                             # allow pulls
dockerfile = "./Dockerfile.agent"                # fallback: build locally
dockerfile_build = true                          # allow builds

# Persistent storage
[containers.persistent_storage]
type = "docker"               # docker (volume), bind, s3, sftp, nfs
path = "archie-data"          # volume name or mount path
```

### Per-repo persistent storage override

```toml
[[repos]]
owner = "sam"
name = "tau"
persistent_storage = { type = "bind", path = "./archie-core-data/projects/tau" }
```

When configured, each task for this repo mounts the same bind path. Memory persists across sessions. The agent writes to `MEMORY.md` in this directory.

### Agent image

A fat Docker image containing:
- archie-agent binary
- NATS client library
- LLM provider SDKs (ai-sdk runtime)
- Base tools: git, gh, go, golangci-lint, gofumpt, node, python, ruff, pytest, etc.
- esbuild (for TypeScript plugin transpilation)
- Yaegi interpreter (bundled in the binary)

Pre-built and published to a registry. The daemon pulls on startup. Users can provide their own image via config.

---

## 5. Extensibility layers

> **Reconciliation note.** This section describes a two-layer plugin architecture (skill-bundled plugins loaded per-task, core daemon plugins loaded at startup) that does not exist in code. What *does* exist — and is implemented, tested, and already in production — is described in the sibling PRD `docs/prds/yaegi-plugin-system.md`, whose three phases (custom gate functions, custom workflow stages, skill scripts) are marked "implemented" and match disk state exactly as of commit `d45f21d`. That implementation is narrower and shaped differently than what follows:
>
> - It has **no skill-bundling concept** — `.archie/gate.go` and `.archie/stages/*.go` are repo-level, not shipped inside a skill's `plugins/` directory; skill scripts (`.archie/skills/<skill>/scripts/*.go`) are the closest analog but none of the five current skills ship a `scripts/` directory yet.
> - It has **no daemon-side core plugin registry** — no `Plugin` interface, no `Register(daemon *Daemon)`, no `~/.config/archie/plugins/` loader. Yaegi today only interprets small, narrowly-scoped `.go` files (gates, stages, scripts) in-process inside `archied`, using generated symbol tables (`internal/gate/gateextract`, `internal/workflow/wfextract`) rather than a general extension-registration interface.
> - Custom stages are **auto-discovered and run in filename order**, not routed by name from `config.toml` — the workflow engine has no data-driven stage graph today, so the `[[workflows.stages]]` config sketch below does not correspond to real config.
>
> Before implementation begins, decide: does Layer 1/Layer 2 as described below *replace* the existing three Yaegi surfaces, or does it *extend* them (e.g., teach the existing loaders to also read from a skill's `plugins/` directory, and add a genuinely new daemon-side `Plugin` interface as a separate, later addition)? The existing surfaces are working code with real symbol-table generation and panic recovery — treat them as the implementation substrate for Layer 1, not as something to discard.

### Layer 1: Skills (`archie-wf-*`)

> **Status: cosmetic only.** Five `SKILL.md` files exist today (`ecosystem-node`, `ecosystem-python`, `go-quality-gate`, `security-audit`, `tdd-bugfix`) at `.archie/skills/<name>/SKILL.md` — a single flat repo-relative location, no worktree-vs-user-level split, no `archie-wf-*` naming. Each has a `metadata.archie` block with only `tools` and `engine` keys (no `plugins`, no `budget_usd`). **No Go code parses SKILL.md frontmatter, discovers skills, or does progressive disclosure** — grepping for a skill loader, catalog, or frontmatter parser across the repo returns nothing. The workflow `Mission` strings that drive agent behavior today are hand-written Go string literals in `internal/workflow/{implement,tdd,feasibility}.go`, not content injected from SKILL.md. The stage names described in the PRD's SKILL.md prose closely mirror the hardcoded Go stage names (one drift found: `tdd-bugfix/SKILL.md` says the last stage is "deliver"; the code calls it `open-pr`) — these read as retrofitted documentation of existing v1 behavior wearing v2-flavored frontmatter, not a functioning specification the daemon consumes.

Skills are Markdown documents following the [agentskills.io](https://agentskills.io/specification) specification with archie-specific extensions.

#### Discovery

```
.agents/skills/archie-wf-tdd/SKILL.md        (worktree — highest precedence)
.agents/skills/archie-wf-implement/SKILL.md
.agents/skills/archie-wf-feasibility/SKILL.md
~/.archie/skills/archie-wf-default/SKILL.md   (user-level — fallback)
```

The agent discovers skills at workflow start. The skill matching the task's workflow type is loaded first. If absent, falls back to `archie-wf-default`. If that's absent too, the agent works from AGENTS.md conventions and best practices.

#### Extended agentskills.io format

Skills follow the standard spec (`name`, `description`, `version`, `SKILL.md` body) with archie-specific metadata:

```yaml
---
name: archie-wf-tdd
description: >
  Test-driven bugfix workflow for archie-core. Reproduce the bug with
  failing tests, prove the repro, fix the code without touching tests.
version: 1.0.0
metadata:
  archie:
    plugins:                     # bundled Yaegi plugins shipped with this skill
      - plugins/custom-gate.go   # repo-specific gate logic
      - plugins/gosec-check.go   # security scanner integration
    tools: [go, golangci-lint, gofumpt, task]
    engine: any
    budget_usd: 2.00
---
```

#### Progressive disclosure

Per the agentskills.io spec:
1. **Catalog** (~100 tokens): `name` + `description` loaded at workflow start
2. **Instructions** (<5000 tokens): Full `SKILL.md` body when the skill is activated
3. **Resources** (as needed): References, scripts, and bundled plugins loaded on demand

#### Bundled plugins

A skill's `plugins/` directory contains Yaegi `.go` files. These are loaded by the agent at workflow start alongside the skill instructions. The daemon copies them to `/data/plugins/` before the container starts.

If `/data/plugins/` already has a plugin with the same name (from persistent project storage), the volume version wins. Conflicts are logged by the daemon.

### Layer 2: Core plugins

> **Status: aspirational, zero code.** No `Plugin` interface, no plugin registry, no `~/.config/archie/plugins/` loader exists anywhere in the repo (confirmed by grep for `Plugin`, plugin registry, and `Register(daemon` — zero matches). Yaegi is a real dependency (`traefik/yaegi v0.16.1`) and is genuinely wired up, but only for the three narrow surfaces in §5's reconciliation note above, all interpreted in-process inside `archied` — not for extending the daemon with new forge/ticketing/storage/secrets/notify implementations as described below.

Core plugins live at `~/.config/archie/plugins/` and extend the daemon itself:

```
~/.config/archie/plugins/
├── forge-gitea.go         # Gitea forge implementation
├── forge-gitlab.go        # GitLab forge implementation
├── ticketing-jira.go      # Jira ticketing integration
├── ticketing-linear.go    # Linear ticketing integration
├── storage-s3.go          # S3 persistent storage backend
├── vault-secrets.go       # HashiCorp Vault integration
└── notify-servicenow.go   # ServiceNow integration
```

#### Loading

Loaded by the daemon at startup via Yaegi. Each plugin registers itself with the daemon's extension registry:

```go
// Plugin interface (Go, interpreted via Yaegi)
type Plugin interface {
    Name() string
    Version() string
    Register(daemon *Daemon) error
}
```

The daemon discovers `.go` files, evaluates them, extracts the plugin symbol, and calls `Register()`. Failed plugins are logged and skipped — the daemon starts with remaining plugins.

#### What core plugins can do

- **Forges** — implement the `forge.Forge` interface. Register themselves as available forge types.
- **Ticketing** — implement a ticketing interface. Poll external systems, format issues into the standard `forge.Issue` struct.
- **Storage backends** — implement a storage interface. Provide volume population and cleanup.
- **Secrets** — implement a vault interface. Inject credentials into `/data/secrets/` before agent start.
- **Notifications** — implement a notify interface. Deliver PRDs, status updates beyond the built-in webhook.

---

## 6. Task lifecycle (v2)

> **Status: aspirational shape, real states.** The status values (`pr_open`, `merged`, `rejected`, `parked`, etc.) and stage names are real — they're the actual `store.Task.Status`/`Stage` values used today. What's aspirational is the *execution model* implied by this diagram: today there is no JetStream publish step, and "Agent consumes" is really "the daemon's single poll-loop goroutine claims the next queued row from SQLite and runs the whole workflow synchronously in-process" (`daemon.Cycle()` → `store.ClaimNext` → `process()` → `workflow.Run()`). There is no concurrent multi-agent consumption today — see §0 and §2.

```
Discover ──→ Publish to JetStream ──→ Agent consumes ──→ running(workflow:stage)
                                                              ↓
                                                        pr_open → merged | rejected
                                                              ↓
                                                        waiting_human → (approved) → requeue
                                                              ↓
                                                         parked
```

### Detailed flow

1. **Discover** — daemon polls forges for labelled/assigned issues
2. **Publish** — daemon publishes task to `archie.task.<workflow_type>` JetStream subject
3. **Spawn** — daemon creates task volume, writes `task.json`, stages bundled plugins
4. **Start container** — `docker run` with volume mounts, network (NATS), resource limits
5. **Agent boots** — reads `task.json` from `/data/`, connects to NATS, discovers skills, loads bundled plugins
6. **Run workflow** — agent executes routed workflow stages. Reports progress to system channel. Result to response channel.
7. **Daemon reviews** — reads agent response. Forwards to human channels (issue comment, label update, PR). System messages stay internal.
8. **Grace period** — agent stays alive for `max_uptime`. Handles follow-ups (gate re-runs, human replies forwarded by daemon).
9. **Kill** — daemon kills container. Volume cleaned up on terminal outcome or TTL expiry.

---

## 7. Agent → daemon → human flow

```
┌─────────┐  response   ┌─────────┐  reviewed   ┌──────────┐
│  Agent  │────────────→│  Daemon │────────────→│  Human   │
│         │  system     │         │  queried    │ channels │
│         │────────────→│         │←────────────│          │
└─────────┘             └─────────┘             └──────────┘
                              │
                              ├── Issue comments (parked, PR, waiting)
                              ├── Label updates (archie:queued → archie:working → archie:pr)
                              ├── Notify webhook (feasibility PRD delivered)
                              └── SSE dashboard (real-time observability)
```

The agent writes whatever it wants to `response`. The daemon reviews it. No agent output reaches a human unprompted. The `system` channel is purely internal — the daemon reads it for observability and debugging, never forwards it.

---

## 8. Implementation phases

> Updated 2026-07-22 against actual repo state (see §0). Items marked **done** are already merged; nothing else in this document is implemented beyond them. Phase ordering below is unchanged from the original sketch except Phase 3, which now builds on top of existing Yaegi work instead of assuming a blank slate.

### Phase 1: Binary split + NATS
- ~~Split `archied` into daemon binary~~ — **done**, `cmd/archied` exists
- ~~Create `archie-agent` binary~~ — **done, but partial**: `cmd/archie-agent` exists and executes one workflow *stage* per invocation over a versioned stdin/stdout JSON protocol (`internal/agentexec`, protocol v1). Still needed: promote it from a single-stage subprocess worker to the PRD's per-task process, and move workflow routing/sequencing (`workflow.Route`/`workflow.Run`) out of `archied` and into it.
- Add NATS to both (JetStream for task queue, request-reply for comms) — not started. `internal/events.Bus` (in-process pub/sub, already feeding SQLite + SSE) is the natural seam to replace the transport under.
- Daemon publishes, agent consumes, results return via NATS — not started
- Current single-process mode preserved for development (`--local` flag) — effectively already true: `inprocess` `[agent] mode` is the *default*, not an opt-in fallback, so this constraint is satisfied but should be revisited once subprocess/container modes are the default.

### Phase 2: Docker sandbox
- Docker agent image (fat, with tools) — not started
- Daemon container lifecycle: spawn, volume create, env inject, kill — not started
- Persistent volumes with TTL — not started (current worktree lifecycle is fresh-clone-per-task with status-driven cleanup, no TTL)
- `max_concurrency` enforcement — not started (daemon processes one task at a time today)
- Grace period `max_uptime` — not started
- **Groundwork already in place:** `agentexec.Runner` (`InProcessRunner`/`SubprocessRunner`) is an interface designed, per its own doc comments, to be swapped for a container-backed runner without touching workflow orchestration — implement `ContainerRunner` against this interface rather than redesigning the execution boundary.

### Phase 3: Skills + bundled plugins
- agentskills.io spec implementation (progressive disclosure) — not started; current SKILL.md files are unparsed documentation (see §5)
- `archie-wf-*` skill discovery from worktree and user paths — not started; current skills live at a single flat `.archie/skills/` path with different naming
- Bundled plugin loading via Yaegi from skill's `plugins/` directory — not started, but **has a working substrate to extend**: `internal/gate/gateeval`, `internal/workflow/wfeval`, and `internal/skillscript` already interpret `.go` files via Yaegi with generated symbol tables and panic recovery (see `docs/prds/yaegi-plugin-system.md`, Phases 1–3, done). This phase should teach those loaders to also discover files from a skill's `plugins/` directory, not build a second Yaegi integration from scratch.
- Volume-staged plugins with conflict resolution — not started, and depends on Phase 2's volume work existing first

### Phase 4: Core plugins
- Yaegi plugin loader for `~/.config/archie/plugins/` — not started (no `Plugin` interface, no registry, no loader exist today)
- Plugin registry in daemon — not started
- Gitea forge as first core plugin (proof of concept) — not started (current `internal/forge` only implements GitHub REST polling)
- Linear ticketing as second core plugin — not started

### Phase 5: Pluggable storage
- Docker volume backend (built-in) — not started
- Bind mount backend (built-in) — not started
- S3, SFTP, NFS backends as core plugins — not started

---

## 9. Open questions

1. **Agent image versioning:** Use tagged versions (`:v1.2.3`) with a "never repoint tags" policy. `:latest` is for dev only, not the config default. No digest complexity needed — archie-core is self-hosted, not a public registry.

2. **NATS auth:** Anonymous for single-node deployments (daemon + NATS + agents on same host). Credentials required for multi-node/cluster. Add an optional `[nats]` config block (default: anonymous, validated only when present).

3. **Worktree ownership:** ~~Answered by current implementation.~~ Fresh `git clone` per task attempt, no shared cache. This is fine for v2. Revisit if concurrent agents on the same repo become a bottleneck.

4. **Grace period interaction:** Reset `max_uptime` on human reply — the agent just did useful work. But cap total lifetime with `max_total_uptime` (e.g. 4 hours) to prevent infinite hanging. `max_uptime` = grace after inactivity, `max_total_uptime` = absolute kill switch.

5. **Concurrent agents on the same repo:** Blocked (worktree conflict) by default. For repos where concurrent work is safe (different base branches, different packages), add a per-repo override: `allow_concurrent = true`. Resolve during Phase 2.

6. **Plugin architecture reconciliation:** Extend the existing Yaegi surfaces (`internal/gate/gateeval`, `internal/workflow/wfeval`, `internal/skillscript`) rather than building a parallel `Plugin.Register(daemon)` interface. Core plugins follow the same pattern: `Eval()` a `.go` file, extract known symbols. Settled before Phase 3/4.

7. **Skill loading scope:** "Parse frontmatter, inject body into system prompt" is sufficient for v2. The gap is that nothing reads SKILL.md today — closing that gap is the value. Full progressive disclosure (catalog → activate → resources) is deferred to a later phase.
