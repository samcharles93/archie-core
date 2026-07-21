# archie-core v2 Architecture — Design Document

**Author:** Archie (Hermes agent)  
**Date:** 2026-07-21  
**Status:** Draft — interview-complete, pending final review

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

### archie-agent (ephemeral container)

Spawned per task. Lives in a Docker container. Receives its brief via `/data/task.json` (volume-mounted). Owns:

- **Workflow execution** — runs the routed workflow stages (implement, tdd, feasibility, bootstrap)
- **LLM orchestration** — via `ai-sdk/agentloop`, the agent drives LLM calls
- **Bundled plugin loading** — loads Yaegi `.go` plugins from the skill's `plugins/` directory and the `/data/plugins/` volume
- **Skill loading** — reads `.agents/skills/archie-wf-*` from the worktree, follows the agentskills.io progressive disclosure model
- **NATS reporting** — publishes results to `archie.agent.<task_id>.response`, system messages to `archie.agent.<task_id>.system`

Lifecycle: spawn → load task → run workflow → report → wait for follow-ups (grace period) → killed by daemon.

---

## 2. NATS subject hierarchy

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

### Layer 1: Skills (`archie-wf-*`)

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

### Phase 1: Binary split + NATS
- Split `archied` into daemon binary
- Create `archie-agent` binary (extracted agent loop from current daemon)
- Add NATS to both (JetStream for task queue, request-reply for comms)
- Daemon publishes, agent consumes, results return via NATS
- Current single-process mode preserved for development (`--local` flag)

### Phase 2: Docker sandbox
- Docker agent image (fat, with tools)
- Daemon container lifecycle: spawn, volume create, env inject, kill
- Persistent volumes with TTL
- `max_concurrency` enforcement
- Grace period `max_uptime`

### Phase 3: Skills + bundled plugins
- agentskills.io spec implementation (progressive disclosure)
- `archie-wf-*` skill discovery from worktree and user paths
- Bundled plugin loading via Yaegi from skill's `plugins/` directory
- Volume-staged plugins with conflict resolution

### Phase 4: Core plugins
- Yaegi plugin loader for `~/.config/archie/plugins/`
- Plugin registry in daemon
- Gitea forge as first core plugin (proof of concept)
- Linear ticketing as second core plugin

### Phase 5: Pluggable storage
- Docker volume backend (built-in)
- Bind mount backend (built-in)
- S3, SFTP, NFS backends as core plugins

---

## 9. Open questions

1. **Agent image versioning:** Does the daemon pull `:latest` and auto-restart, or pin to a digest?
2. **NATS auth:** Anonymous for single-node, or credentials for multi-node/cluster?
3. **Worktree ownership:** Does the daemon clone repos into a shared volume that agents mount read-only, or does each agent clone fresh?
4. **Grace period interaction:** If a human replies during grace period and the agent handles it, is the grace period reset?
5. **Concurrent agents on the same repo:** Blocked (worktree conflict) or allowed with different base branches?
