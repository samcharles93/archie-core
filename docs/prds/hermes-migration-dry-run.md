# Hermes → archie-core Migration: Dry-Run Report

**Bead:** archie-core-abg.39
**Source:** `.hermes-carina/` (live copy of the Hermes deployment on host `carina`, gitignored)
**Status:** dry-run inventory + mapping proposal only. No data has been copied or imported.

This is the first acceptance-criteria deliverable for abg.39 ("produce a dry-run
report"). It inventories what exists in the live Hermes state, proposes where each
category lands in archie-core, and flags what must never be copied. It does **not**
implement an importer — see "Blocked on" below for why that's a separate, later step.

## Environments discovered

Hermes runs four environments under `.hermes-carina/`, matching the original gap
analysis's summary:

| Environment | Location | Bot identity purpose |
|---|---|---|
| `main` (root) | `.hermes-carina/` top level | Primary assistant, all-channel |
| `agile-lead` | `.hermes-carina/profiles/agile-lead/` | Project/PM-focused persona |
| `dev` | `.hermes-carina/profiles/dev/` | Development-focused persona |
| `local-agent` | `.hermes-carina/profiles/local-agent/` | Local-only, no external channels (`.no-bundled-skills` marker present) |

Each environment directory has the same internal shape: `SOUL.md`, `config.yaml`
(+ timestamped backups), `.env`, `auth.json`, `channel_directory.json`,
`gateway_state.json`, `cron/`, `memories/`, `sessions/`, `skills/`, `platforms/`,
`workspace/`, `home/`.

## State inventory (counts only, no content copied here)

| Environment | Skills | Session keys | Cron jobs |
|---|---|---|---|
| main | 34 dirs under `skills/` | 419 (`sessions/sessions.json`, excl. `_README`) | 2 (`cron/jobs.json`) |
| agile-lead | 20 | 2 session files under `sessions/` | present, not yet counted |
| dev | 25 | 2 session files under `sessions/` | present, not yet counted |
| local-agent | 17 | 0 | present, not yet counted |

Session keys follow the pattern `agent:<profile>:<platform>:<kind>:<identifier>`
(e.g. `agent:main:telegram:dm:<user_id>`, `agent:main:email:dm:<address>`) — this
naming convention is the natural key for archie-core's own
`gateway.SessionStore` (`internal/gateway/session_store.go`), which already has
`Save`/`Get`/`GetByChannel`/`List` methods shaped for exactly this.

## Proposed mapping to archie-core

| Hermes concept | archie-core target | Notes |
|---|---|---|
| Environment (`main`, `agile-lead`, `dev`, `local-agent`) | `config.IdentityConfig` (archie-core-abg.37) | Closest existing primitive: isolated forge/models/repos. archie-core has **no profile-scoped gateway/tool/memory/workspace composition yet** — see "Blocked on" below. |
| `SOUL.md` | New: a per-identity system-prompt block, analogous to `memory.Manager.SystemPromptBlock()` (`internal/memory/manager.go`) but for persona rather than memory | No existing field carries this today. |
| `.env`, `auth.json`, `config.yaml` secrets, `mcp-tokens/` | **Excluded — never imported.** Reconfigured manually per-identity via `internal/secret` (archie-core-xec) `SecretRef`s. | See "Excluded from migration" below. |
| `memories/` | `internal/memory.Manager` (built-in file-backed provider already exists: `internal/memory/builtin`) | Format differs (Hermes memory provider schema vs. archie-core's MEMORY.md/USER.md) — needs a translator, not a raw copy. |
| `skills/*/` (SKILL.md + supporting files) | `internal/skill.Catalog`/`Discover` (`internal/skill/skill.go`) | archie-core's skill format (SKILL.md frontmatter) is closely related to Hermes's; likely the most direct 1:1 copy of the four state categories, but needs a compatibility pass (Hermes skills assume 80+ built-in tools archie-core doesn't have yet — see `docs/prds/hermes-gap-analysis.md` §"Built-in tools"). |
| `sessions/sessions.json`, per-profile `sessions/` | `gateway.SessionStore` (NellDB-backed) | Needs a JSON→NellDB doc translator; message content history format also differs. |
| `cron/jobs.json` | Not yet — archie-core has **no cron subsystem** (confirmed: `internal/daemon` has a ticker poll loop, not a general scheduler). Tracked separately under the gap analysis's cron/scheduler gap (P1, archie-core-abg.24's territory). | Import blocked until a cron primitive exists. |
| `channel_directory.json`, `platforms/` | Partially: `config.ChatConfig`/`TelegramConfig`/`EmailConfig` (`internal/config/config.go`) for the channels archie-core already supports (Telegram, email, webhook). Everything else (Discord, Slack, Matrix, etc.) has no archie-core equivalent yet. | |
| `plugins/` (e.g. `hermes-achievements`) | `internal/plugin` (Yaegi) or a new typed engine, per the "Plugin engine rule (strict)" invariant now in `ARCHITECTURE.md` | Each plugin needs individual evaluation — not a bulk copy. |

## Excluded from migration (never read into an import, never committed)

- `.env` (all environments) — API keys, provider credentials.
- `auth.json` (root and per-profile) — OAuth/session tokens.
- `mcp-tokens/` — MCP server credentials.
- `config.yaml` fields matching `key`/`token`/`secret`/`password`/`credential` (the file mixes structural config with provider credentials in the same document; a real importer must field-filter, not copy the file wholesale).
- `.hermes_history` (shell history — may contain pasted secrets).
- `cache/`, `audio_cache/`, `disk-cleanup/` — transient, not identity state.

This list itself was produced without reading any of the excluded files' contents —
only directory listings and structural (non-secret) YAML top-level keys were
inspected to write this report.

## Blocked on

Building the actual importer now would produce inert data: archie-core's chat
runtime is still the one-shot `runtime.Chat(MaxSteps: 1)` responder with no tools,
memory, or skills wired into a conversation turn (archie-core-abg.36, not yet
implemented — see that bead). Importing SOUL/memory/skills before there's a
runtime that consumes them per-turn means the imported state sits unused. The
recommended order (matches both the original gap-analysis audit and the
capability-host plan already in beads memory `plugin-engine-rule`):

1. archie-core-abg.37 (multi-identity composition) — in progress, daemon-side
   routing complete; unblocks "which identity" for imported state to attach to.
2. archie-core-abg.36 (gateway agent loop with tools/memory/skills) — not started;
   the consumer that would make imported SOUL/memory/skills meaningful.
3. **Then** abg.39's actual import tooling: a `internal/hermesimport` (or similar)
   package with an explicit allowlist of source fields (never a directory copy),
   a dry-run mode that reports what *would* be written without writing it, and a
   rollback-safe cutover checklist for host `carina` (see the
   `two-archied-instances-run-on-host-carina-ssh` beads memory for the live
   deployment topology this must not disrupt).

## Next steps for this bead

- [ ] Design the SOUL→system-prompt-block field (needs a decision: new `Daemon`
      field, or folded into the existing `memory.Manager.SystemPromptBlock()`
      path?).
- [ ] Design the memories/ and sessions/ translators once abg.36 defines what a
      chat turn actually consumes.
- [ ] Write the field-allowlist import tool (no raw file/directory copies).
- [ ] Shadow-run plan: run archie-core alongside Hermes on `carina` without
      stopping Hermes, compare outputs, before any cutover.
