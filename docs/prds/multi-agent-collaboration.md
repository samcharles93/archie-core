# Multi-Agent Collaboration -- PRD

**Status:** Draft (discovery interview complete, 2026-07-25)
**Author:** Discovery interview (archie-core-abg.1), synthesized from Sam's answers
**Parent epic:** archie-core-abg

---

## 0. Framing

Today, "running multiple agents" means running multiple independent `archied`
processes on the same host (`carina`): `archie` (bot_user=archie, repos
sam/archie-core + sam/tau, deepseek models) and `winter` (bot_user=winter,
repo sam/tau only, local llama.cpp models). Confirmed by diffing
`~/.config/archie/config.toml` and `~/.config/archie/winter.toml` on carina:
they differ in `bot_user`, `[[repos]]` list, and `[models]`/`[providers]` --
not just identity. `Config.BotUser` is a single global field, so one process
= one identity today.

The goal stated for archie-core overall is to become a fully extensible
agentic workhorse -- comparable to and improving on OpenClaw/Hermes. This PRD
scopes the "multi-agent" slice of that: what it takes to go from N processes
to one daemon that can do what N processes do, plus the surrounding
capabilities (chat channels, plugins, memory) needed to reach that bar.

## 1. Scope (confirmed in interview)

All of the following are **in scope** for the epic (not necessarily the first
implementation slice -- see section 6):

1. **Daemon-side concurrency** -- one daemon process running multiple bot
   identities concurrently, replacing N separate `archied` processes.
2. **Cross-repo / cross-config flexibility** -- identities are not statically
   bound to one repo list at process startup; repo/model/provider
   configuration varies per identity within a single daemon.
3. **Agent-to-agent collaboration on a single task** -- one agent's output
   feeding another (e.g. plan -> implement -> review handoff), not just N
   identities running in parallel unaware of each other.
4. **Conversational access** -- chat channels (Telegram, web UI, more via
   plugins) where Sam talks to archie directly, outside the Gitea
   issue-driven flow. Confirmed requirement: **full parity** with the
   issue-driven flow -- chat is an alternate entry point into the same
   triage/planner/builder dispatch pipeline, not a separate toy Q&A surface.
   Chat must be able to: answer questions read-only, spawn/control real work
   (start an agent, check status, approve/reject a PR, cancel a run), and
   drive the same pipeline Gitea assignment drives today.
5. **General extensibility** -- plugins/channels/integrations should be
   addable, not hardcoded:
   - Additional forges beyond GitHub/Gitea (e.g. GitLab) via the existing
     `forge.Forge` interface, as Layer 2 core plugins (see
     `docs/prds/archie-core-v2-architecture.md` sec.5).
   - MCP server connections (archie as an MCP client, to consume external
     tools/data sources).
   - SSH / remote execution as an agent capability.
   - Arbitrary API integrations, callable by agents as tools.
   - Additional chat channels beyond Telegram/web UI, as plugins.
6. **Memory systems for multi-session chat** -- conversations with archie
   need to persist across sessions (distinct from the existing per-task
   `/data/memory.jsonl` sketch in the v2 PRD, which is task/project scoped,
   not conversation scoped).
7. **Slash-command capabilities** in chat channels (e.g. `/status`, `/spawn`,
   `/approve`), analogous to what tau/Telegram-style bots expose today.
8. **Multi-user support**, as a capability the architecture should not
   foreclose -- "if I want to extend my agent to multiple people, I should be
   able to." Not required to ship in the first slice (see section 5), but
   the identity/session model must not assume single-operator forever.

## 2. Explicitly out of scope (for now)

- **Voice/audio interfaces.** Text-based chat only.
- **Building GitHub support** -- it already exists (`internal/forge`
  implements GitHub REST polling per `ARCHITECTURE.md`).
- Anything not listed in section 1 that isn't a natural extension of the
  plugin/forge/channel interfaces below.

Note: multi-user auth and GitLab support are **in scope as capabilities the
architecture must support**, but are not committed to the first
implementation slice -- see section 5.

## 3. Constraints from existing architecture

From `ARCHITECTURE.md` and `docs/prds/archie-core-v2-architecture.md`
(read in full before implementation; summarized here):

- **`Config.BotUser` is a single global field** (`internal/config`) -- the
  root blocker for daemon-side multi-identity. Becomes a per-identity field
  in a new config shape (see section 4).
- **NATS/JetStream task distribution is implemented**: `internal/nats`
  publishes tasks to `archie.task.<workflow_type>` subjects; agents
  (in-process, subprocess, or Docker containers per `internal/container`)
  consume and report back over `archie.agent.<task_id>.{response,system,request}`.
  This subject hierarchy is a natural fit for routing by identity too
  (e.g. `archie.<identity>.task.<type>`) but that's a design decision, not
  yet made.
- **Dispatch is `daemon.Run()` polling per configured forge/repo list**,
  serialized per `owner/repo` by default (`AllowConcurrent` opts out).
  Multi-identity daemon means multiple poll loops (or one loop iterating
  multiple identity configs) feeding the same NATS/container dispatch
  machinery.
- **Layer 2 core plugins (`~/.config/archie/plugins/`, Yaegi-loaded,
  `Plugin.Register(daemon)`) are designed but not wired** -- interface
  exists (`internal/plugin/plugin.go`), no loader in `cmd/archied/main.go`.
  This is the natural extension point for new forges, MCP client
  connections, and chat channel plugins. Building the multi-agent epic on
  top of an unwired plugin layer means **wiring Layer 2 is a prerequisite**,
  not parallel work.
- **No chat/messaging code exists today** -- no Telegram, no web UI beyond
  the read-only SSE dashboard (`internal/webui`). This is greenfield.
- **The daemon gatekeeps all agent output** -- "no agent output reaches a
  human without daemon review" (`archie-core-v2-architecture.md` sec.7).
  Chat channels must respect this: a chat message that triggers a spawn goes
  through the same review/labeling machinery PRs and comments do today, not
  a side channel that bypasses it.
- **Environmental constraints over prompt rules** (`ARCHITECTURE.md` design
  decision #1) -- chat-triggered actions (spawn, approve, cancel) should be
  enforced by code (permission checks, rate limits), not by asking the LLM
  nicely.

## 4. Identity model (design direction, not yet finalized)

Confirmed requirement: within one daemon, identities differ by bot identity,
repo list, *and* model/provider config (per the archie/winter diff). Proposed
shape -- **not yet approved, needs a follow-up design pass before
implementation**:

```toml
# ~/.config/archie/config.toml (sketch)
[[identities]]
name = "archie"
bot_user = "archie"
forge = { type = "gitea", host = "...", token_env = "..." }
models = { triage = "...", planner = "...", builder = "..." }
repos = [ ... ]

[[identities]]
name = "winter"
bot_user = "winter"
forge = { type = "gitea", host = "...", token_env = "..." }
models = { triage = "...", planner = "...", builder = "..." }
repos = [ ... ]
```
```

**Design answer (2026-07-25): per-identity goroutine, shared infrastructure.**

Each identity gets its own goroutine running an independent poll loop with
its own forge client, worktree manager, and NATS subject namespace
(`archie.<identity>.task.<type>`). They share only the infrastructure
pieces where isolation adds cost without benefit:

| Shared (one instance for all identities) | Per-identity (one per goroutine) |
|---|---|
| SQLite store (`internal/store`) | Forge client (different token per identity) |
| NATS connection (`internal/nats`) | Worktree manager (different `BotUser` per identity) |
| Container pool (`internal/container`) | Config subset: forge, repos, models, providers, dispatch, poll_interval, bot_user, bot_email |
| Event bus (`internal/events`) | NATS subject namespace: `archie.<identity>.task.<type>` |
| Web UI (`internal/webui`) | Poll loop goroutine |
| Plugin registry (`internal/plugin`) | |
| Gateway router (`internal/gateway`) | |

Decision rationale: per-identity goroutines guarantee failure isolation (one
identity's forge outage cannot block another's poll tick), each identity
gets its own forge rate-limit domain, and the existing `Daemon` struct maps
cleanly  --  each goroutine holds a thin `Identity` value (forge client + config
subset + worktree manager + NATS subject prefix) and dispatches tasks with
an `identity_name` column on the task row. The shared NATS connection
multiplexes subjects by identity prefix; the shared store already handles
concurrent ClaimNext via SQLite's serialized writes. NATS subjects carry
the identity name so agent containers (which consume from NATS) route
responses back to the correct daemon identity.

This design resolves the open question in section 4 of the original PRD
draft (per-identity goroutine vs single loop iterating identities  --  the
former, for failure isolation).

## 5. Priority / phasing

Sam deferred ordering to this PRD (interview answer: "let the interview
settle scope, I'll decide after seeing it written up"). Proposed
dependency-ordered phases, for Sam to confirm or reorder:

**Phase A -- Foundation (blocks everything else)**
- Wire Layer 2 core plugin loading (`~/.config/archie/plugins/`,
  `Plugin.Register(daemon)`) into `cmd/archied/main.go`. Currently designed,
  unwired -- both chat channels and new forges depend on this existing.

**Phase B -- Multi-identity daemon core**
- Replace single `Config.BotUser` with the `[[identities]]` shape (section
  4, after design confirmation).
- Multi-identity poll/dispatch, still issue-driven (Gitea only, no chat yet).
- Migrate the two carina processes (archie, winter) onto one daemon as a
  real-world validation.

**Phase C -- Chat channel foundation**
- Chat channel plugin interface (parallel to `forge.Forge`).
- Telegram channel plugin: read-only Q&A against task/daemon state first,
  then spawn/control actions gated through the same review pipeline as
  issue-driven dispatch.
- Web UI: extend `internal/webui` from read-only SSE dashboard to
  bidirectional chat.
- Multi-session conversation memory (distinct persistence from per-task
  `/data/memory.jsonl`).
- Slash commands (`/status`, `/spawn`, `/approve`, `/cancel`).

**Phase D -- Agent-to-agent collaboration**
- Plan -> implement -> review handoff within a single task's workflow
  (builds on existing `workflow.Stage` sequencing -- this may already be
  close given the `implement` workflow's plan/build stage split; needs
  investigation into how much is genuinely new vs. reframing).

**Phase E -- Extended integrations (as needed, each is an independent
plugin, no fixed order)**
- GitLab forge plugin.
- MCP client capability (archie consuming external MCP servers as tools).
- SSH / remote execution tool.
- Additional chat channels.
- Multi-user support (identity/session model extended beyond
  single-operator -- needs its own design pass; not blocking Phase A-D).

Sam's explicit instruction was to identify next requirements **and begin
work**, not just deliver a document. Phase A (wiring the Layer 2 plugin
loader) is the correct starting point: it's small, well-specified already in
`docs/prds/archie-core-v2-architecture.md` sec.5, and unblocks both the chat
channel work and new forge plugins that everything else in this PRD depends
on.

## 6. Acceptance criteria for this PRD

- [x] Reflects Sam's actual interview answers, not assumptions.
- [x] Confirms the archie/winter config diff (repo lists differ, not just
      identity).
- [ ] Broken into scoped, dependency-ordered beads issues under
      `archie-core-abg`, the way the 5zh epic was. (Next step, this session.)
