# Hermes Feature Gap Analysis

**Date:** 2026-07-26
**Source:** Parallel investigation of `/work/clones/hermes-agent` by 7 subagents
**Comparison target:** archie-core at commit faf1e38

---

## Scale Comparison

| Metric | Hermes Agent | archie-core |
|---|---|---|
| Primary language | Python 3.10+ | Go 1.26 |
| Source files | ~200+ .py files | ~70 .go files |
| Lines of code | ~500K+ | ~20K |
| Gateway code | ~40 files | 3 files (gateway.go, tasks.go + tests) |
| Platform adapters | 30+ (built-in + plugin) | 1 (Telegram) |
| Built-in tools | 80+ | ~5 (capture, script) |
| Slash commands | 42 | 5 (/status, /help, /provider, /model, /spawn) |
| Plugin system | Full hooks + MCP + middleware | Minimal Yaegi loader |
| Memory | Pluggable providers (Honcho, Mem0) | Ephemeral in-memory notes |
| CLI commands | 60+ | 4 flags |
| Cron/scheduler | 8 modules, 60K lines | None |
| Web dashboard | FastAPI backend | Read-only SSE dashboard |
| API server | OpenAI-compatible REST | None |
| Health/readiness | 6 check types | None |
| Delivery | Router + ledger + dead targets | None |

---

## Gap Inventory by Subsystem

### 1. Gateway & Messaging (CRITICAL)

Hermes gateway (`gateway/run.py`, GatewayRunner) manages full lifecycle: adapter connect/disconnect with reconnect backoff, per-session agent cache (LRU, 128 max, 1h TTL), message dispatch pipeline (authorization → session → profile → agent), streaming delivery, slash-command routing, and graceful shutdown with drain.

| Feature | archie-core status | Priority |
|---|---|---|
| Adapter lifecycle (connect/disconnect/reconnect) | None  --  3-method Gateway interface | **P0** |
| MessageEvent data model (types, media, reply context) | None  --  3-field Message struct | **P0** |
| SendResult with retry/error classification | None  --  fire-and-forget | **P1** |
| Session management (SessionSource + AsyncSessionStore) | None | **P0** |
| Platform config (Platform enum, PlatformConfig) | None | **P0** |
| Streaming delivery (token streaming with edit/draft) | None | **P1** |
| Interactive UX (clarify buttons, approval dialogs, pickers) | None | **P2** |
| EphemeralReply with auto-delete TTL | None | **P2** |
| MessageDeduplicator (TTL-based) | None | **P1** |
| TextBatchAggregator | None | **P2** |
| Code-fence-aware truncation | Line-based only | **P2** |

### 2. Platform Adapters (CRITICAL)

Hermes has 30+ platform adapters: Telegram, Discord, Slack, Matrix, WhatsApp, Signal, WeChat, BlueBubbles, QQ, Yuanbao, Webhook, MS Graph, API Server, SMS, Email, IRC, Home Assistant, and plugin-based ones.

| Feature | archie-core status | Priority |
|---|---|---|
| BasePlatformAdapter ABC (20+ methods, capability flags) | None  --  3-method Gateway interface | **P0** |
| Platform adapter registry + plugin discovery | None | **P0** |
| Media handling (image/video/audio/document send) | None | **P1** |
| Typing indicators | None | **P2** |
| Message edit/delete | None | **P2** |

### 3. Agent Loop & Tools (CRITICAL)

Hermes has: central tool registry with 80+ tools, progressive tool disclosure (tool_search/tool_describe/tool_call bridges), MCP client (stdio/HTTP/SSE), tool result persistence with spill-to-disk, per-turn budget enforcement, guardrails (loop detection, idempotent/mutating classification), parallel tool dispatch with gating rules.

| Feature | archie-core status | Priority |
|---|---|---|
| Central tool registry with dynamic discovery | None | **P0** |
| MCP client (stdio/HTTP/SSE transports) | None | **P0** |
| Progressive tool disclosure | None | **P1** |
| Tool result persistence / budget enforcement | None | **P1** |
| Guardrails (loop detection, classification) | None | **P1** |
| Subagent delegation (parallel batch, result summarization) | None | **P2** |
| Browser automation tools | None | **P2** |
| Web search/extraction tools | None | **P2** |
| File operations tools | None | **P2** |
| Image/vision/audio/video tools | None | **P2** |
| Context compression pipeline | None | **P2** |

### 4. Memory & State (HIGH)

Hermes has: pluggable MemoryProvider ABC, built-in file-backed memory (MEMORY.md/USER.md), background sync pipeline, session lifecycle hooks, SQLite+FTS5 state store, session splitting on compression.

| Feature | archie-core status | Priority |
|---|---|---|
| MemoryProvider ABC + plugin registration | None | **P0** |
| Built-in file-backed memory (add/replace/remove) | Ephemeral memoryNotes only | **P0** |
| Background sync pipeline (async writes) | None | **P1** |
| Session lifecycle hooks (on_session_end, on_session_switch) | None | **P1** |
| SQLite+FTS5 conversation history | None | **P1** |
| Session splitting on compression | None | **P2** |

### 5. Security & Auth (HIGH)

Hermes has: PairingStore (cryptographic code generation, salted hashing, OWASP-compliant), GatewayAuthorizationMixin (layered auth: allowlists → role auth → relay delegation → pairing → deny), SlashAccessPolicy (admin/non-admin command gating), secrets CLI (1Password integration, credential lifecycle), rate limiting.

| Feature | archie-core status | Priority |
|---|---|---|
| Pairing/device auth system | None | **P1** |
| Multi-platform authorization mixin | None | **P1** |
| Slash command access control | None | **P2** |
| Rate limiting (per-user, brute-force lockout) | None | **P1** |
| Secrets lifecycle management | None | **P2** |
| API server key-auth guard | None | **P2** |

### 6. CLI & Config (HIGH)

Hermes has: 60+ CLI commands, TUI interface, command registry with plugin contributions, YAML config with corruption resilience, .env file support, self-update/uninstall, setup wizards.

| Feature | archie-core status | Priority |
|---|---|---|
| Interactive CLI with command registry | None  --  4 flags | **P1** |
| Config subcommands (get/set/unset/edit/wizard) | None | **P1** |
| .env file support | None | **P2** |
| Self-update mechanism | None | **P2** |
| Setup wizard | None | **P2** |

### 7. Profiles (HIGH)

Hermes has full multi-profile isolation: `hermes profile create/clone/delete/list/use`, per-profile directories (config, memory, sessions, skills, cron, gateway), profile routing for gateway messages, container-boot profile reconciliation.

| Feature | archie-core status | Priority |
|---|---|---|
| Multi-profile isolation | None | **P1** |
| Profile routing for gateway | None | **P1** |
| Container-boot profile reconciliation | None | **P2** |

### 8. Scheduling & Automation (MEDIUM)

Hermes has: cron scheduler (job store, ticker, execution engine, blueprints with typed slots, lifecycle guard), cron delivery routing, cross-process file locking.

| Feature | archie-core status | Priority |
|---|---|---|
| Cron/scheduling system | None | **P2** |
| Blueprints with typed slots | None | **P2** |
| Lifecycle guard (blocks dangerous commands) | None | **P2** |

### 9. Observability & Operations (MEDIUM)

Hermes has: health checks (state_db, config, disk, model, gateway, background_queues), detailed health endpoint, loop liveness watchdog, systemd notify, drain control with instantiation epoch, scale-to-zero idle detection, delivery ledger with crash recovery.

| Feature | archie-core status | Priority |
|---|---|---|
| Readiness probes (multi-check) | None | **P1** |
| Loop liveness watchdog | None | **P2** |
| Drain control | None | **P2** |
| Scale-to-zero | None | **P3** |
| Delivery ledger with crash recovery | None | **P2** |

### 10. Web Dashboard & API (MEDIUM)

Hermes has: FastAPI web server (Vite/React frontend), OpenAI-compatible API server (/v1/chat/completions, /v1/responses, session CRUD, runs with SSE streaming), OAuth registration.

| Feature | archie-core status | Priority |
|---|---|---|
| Web dashboard (config, sessions, onboarding) | Read-only SSE dashboard | **P2** |
| OpenAI-compatible API server | None | **P2** |
| Session CRUD + fork | None | **P2** |
| Run management with SSE streaming | None | **P2** |

### 11. Integration Extras (LOW)

| Feature | archie-core status | Priority |
|---|---|---|
| Subscription/billing/entitlement | None | **P3** |
| OAuth2 flows (Spotify, DingTalk, etc.) | None | **P3** |
| Desktop app (Electron) | None | **P3** |

---

## Total: ~60 feature gaps across 11 subsystems

**P0 (must-have for baseline parity):** 7 items  --  adapter lifecycle, message model, session management, platform config, tool registry, MCP client, memory provider ABC

**P1 (near-term):** 13 items  --  send result, streaming, dedup, media handling, guardrails, progressive disclosure, tool persistence, session hooks, history store, pairing/auth, CLI, config subcommands, profiles, readiness

**P2 (medium-term):** 22 items  --  interactive UX, subagents, browser/search/file tools, compression, session splitting, slash access, secrets CLI, cron, dashboard, API server, delivery, drain, scale-to-zero

**P3 (future):** 3 items  --  billing, OAuth, desktop app
