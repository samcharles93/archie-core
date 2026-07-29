---
name: archie-config-and-flags
description: Trace, add, rename, deprecate, or debug Archie configuration and command-line flags from external input through defaults, validation, overlays, environment and secret handling, worker snapshots, deployment passthrough, and the production consumer. Use for internal/config, config.example.toml, config.docker.toml, docker-compose.yml, archied or archie-agent flags, multi-identity settings, mode-parity questions, and reports of a setting that parses but has no effect.
---

# Trace Archie configuration and flags

Treat configuration as a dataflow, not as a struct inventory. Prove every
claimed setting has an input, normalization path, composition seam, and runtime
consumer.

Current evidence date: **2026-07-28**.

Use these status terms exactly:

| Status | Meaning |
| --- | --- |
| **production-wired** | A real entrypoint decodes or accepts the value and a runtime consumer uses it. |
| **partially-wired** | Some modes, scopes, or fields consume it, but parity is incomplete. |
| **test/library-only** | An API and tests exist, but no current binary selects it. |
| **decoded-but-unwired** | Decoding/defaulting succeeds, but no production consumer uses the effective value. |
| **external-production-unknown** | The repository does not contain the deployed effective value. |

Never upgrade a status merely because decoding tests pass.

## Trace one setting before editing it

1. Find every external spelling, Go field, flag, environment name, example, and
   deployment reference.
2. Identify the selected loader or flag set.
3. Read `finalize` and any consumer-side defaults.
4. Follow copies across `ForTask`, `ToConfig`, NATS messages, subprocess
   env selection, container env construction, and identity overlays.
5. Find the production composition site and final behavior-changing read.
6. Find tests for decoding, defaulting, invalid input, production composition,
   and mode parity.
7. Check `config.example.toml`, `config.docker.toml`, `docker-compose.yml`, and
   deployment environment passthrough.
8. Assign one status from the table above.

```bash
rg -n 'LoadOverlay|LoadDir|ForTask|ToConfig|flag\.' \
  internal/config cmd/archied cmd/archie-agent
rg -n 'FIELD_NAME|external_key|ENV_NAME' \
  --glob '*.go' --glob '*.toml' --glob '*.yml' --glob '*.yaml' .
```

## Select the real input path

| Input path | Current behavior | Status |
| --- | --- | --- |
| `archied -config PATH` | Defaults to `$XDG_CONFIG_HOME/archie/config.toml`, or `$HOME/.config/archie/config.toml`; `cmd/archied/main.go` calls `config.LoadOverlay`. | production-wired |
| `archied -config-overlay PATH` | Decodes base TOML, then overlay TOML into the same `Config`, calls `finalize` once. Omitted overlay fields retain base values. | production-wired |
| `config.Load(PATH)` | Loads one TOML and finalizes it; no current binary calls it directly. | test/library-only |
| `config.LoadDir(base, overlay)` | Supports main YAML/TOML plus feature YAML and `conf.d`; no current entrypoint calls it. | test/library-only |
| `config.example.toml` | Load-tested example, not a deployed configuration. | test/library-only |
| `config.docker.toml` | Repository overlay mounted by Compose; still depends on host base config. | production-wired |
| Host base config and environment on `carina` | Mounted from `${HOME}/.config/archie/config.toml`; contents absent from this repository. | external-production-unknown |

Both TOML and YAML decoding are non-strict at the field level. Add explicit
compatibility and typo tests; never use "the file loaded" as evidence that a
key took effect.

## Inventory CLI flags

### Public binaries

| Binary | Flag | Default and effect |
| --- | --- | --- |
| `archied` | `-config PATH` | Computed config path described above. |
| `archied` | `-config-overlay PATH` | Empty; overlay only fields explicitly present in second TOML. |
| `archied` | `-once` | `false`; run one poll/process cycle and exit. |
| `archied` | `-requeue ID` | `0`; values greater than zero requeue one parked/waiting task, then exit unless `-once` also set. |
| `archie-agent` | `-nats-url URL` | Empty; takes precedence over `NATS_URL`. Missing both is fatal. |
| `archie-agent` | `-consumer NAME` | `archie-agent`; names the JetStream durable consumer. |
| both | `-quickchecks N` | Incidental flag registered by `github.com/traefik/yaegi/stdlib` importing `testing/quick`; default `100`. Treat as unowned candidate defect. |

The standalone agent always reads its token from `NATS_TOKEN`; there is no
token flag.

### Internal codesearch helper

`archied workspace-codesearch` bypasses daemon configuration before normal flag
parsing:

| Subcommand | Required flags | Optional flags |
| --- | --- | --- |
| `build` | `--root`, `--index` | none |
| `candidates` | `--index`, `--pattern` | `--literal`, `--case-sensitive` |

Do not expose these helper flags as daemon settings.

## Read the current wiring matrix

### Root daemon and capability settings

| Keys | Default, validation, and consumer | Status |
| --- | --- | --- |
| `work_dir`, `db_path` | Derive from `$XDG_DATA_HOME/archie`, else `$HOME/.local/share/archie`; literal `~` in explicit value not expanded. | production-wired |
| `skills_dir` | Empty falls back to `work_dir` for startup workflow registry. | partially-wired |
| `plugin_dir` | Empty disables daemon plugin loading; configured directory loaded in `cmd/archied`. | production-wired |
| `poll_interval` | Default `60s`; root poll loop uses it, identities may override only interval. | production-wired |
| `label` | No default or validation; used for `dispatch.trigger = "label"` or `"either"`. | production-wired |
| `max_retries` | Default `3`; repository value greater than zero overrides. | production-wired |
| `bot_user`, `bot_email` | Single-identity requires `bot_user`; email defaults by forge type. | production-wired |
| `forge.type`, `host`, `token` | Type defaults `github`, host defaults `https://github.com`; only `github` and `gitea` validate. | production-wired |
| `dispatch.trigger` | Default `assignee`; validates `assignee`, `label`, or `either`. | production-wired |
| `dispatch.ack_reaction` | Default `eyes`; case-insensitive `off` normalizes to empty. | production-wired |
| `dispatch.labels.*` | Missing keys default independently to `agent:*`. | production-wired |
| `models.<role>` | Workflow stages read requested role then fall back to `builder`; triage judge falls back `triage` to `planner`. | production-wired |
| `providers.<name>.class`, `api_key_env`, `base_url` | Builds ai-sdk catalog. `base_url` rejects parse errors, userinfo, query, and fragment. | production-wired |
| `agent.mode` | Default `nats` when containers enabled, otherwise `inprocess`; validates `inprocess`, `subprocess`, or `nats`. | production-wired |
| `agent.command`, `agent.env` | Command defaults `archie-agent`; env entries must be nonblank names without `=`. | partially-wired |
| `budgets.max_steps`, `max_tokens`, `wall_clock`, `gate_max_failures` | No loader defaults; `Budgets` documents zero as disabling. | production-wired |
| `diff_cap_lines` | Zero replaced by `400` in `finalize`; `StageDiffCap` treats `<= 0` as disabled. TOML zero cannot disable; negative value does. | production-wired |
| `web.listen` | Empty becomes `127.0.0.1:8484`; literal `off` disables. No authentication. | production-wired |
| `notify.webhook` | Empty disables; feasibility workflow POSTs human-decision notifications when set. | production-wired |
| `nats.url`, `token_env` | Empty URL keeps SQLite path. Configured token variable must be non-empty. | production-wired |
| `containers.*` | See dedicated table below. | production-wired |
| `chat.*` | See channel table below. | production-wired |
| `memory.provider`, `provider_config`, `session_ttl` | Feature YAML decodes and tests these; `cmd/archied` always starts built-in file provider at `work_dir/memory`. | decoded-but-unwired |
| `tools.mcp_servers` | `cmd/archied` registers daemon-side MCP providers. Empty transport becomes `stdio`; only `stdio` accepted. `url` decoded but unused. | partially-wired |
| `tools.tool_policy.*` | Feature YAML decodes `max_result_chars` and `parallel_execution`; no production read exists. | decoded-but-unwired |
| `indexing.index_dir`, `indexing.db_path` | `finalize` derives paths under `work_dir`; no entrypoint constructs `internal/indexing.Manager`. | decoded-but-unwired |
| `extra` / unknown `conf.d` values | Test/library-only `LoadDir` stores untyped data; no core production consumer. | decoded-but-unwired |

The default `agent.command = "archie-agent"` is not verified subprocess
operation. `SubprocessRunner` expects one JSON invocation on stdin and one JSON
response on stdout; `cmd/archie-agent` is a long-running NATS worker.

### Repository settings

| Keys | Effective behavior | Status |
| --- | --- | --- |
| `owner`, `name` | Required for every declared repo. Single-identity config with zero repos loads but polls nothing. | production-wired |
| `base` | `BaseBranch()` maps empty to `main`, but `daemon.process` pre-clones with raw `repo.Base`. Set it explicitly. | partially-wired |
| `gate` | Argv arrays run in order. Empty entries skipped. TDD inverts last non-empty command as repro test by convention. | production-wired |
| `protect` | Path suffixes enforced by agent runner; not prompt-only rule. | production-wired |
| `ecosystem`, `preflight`, `test_glob` | Resolved preflight/test-glob treats empty ecosystem as Go. Container storage receives raw ecosystem. Go/Python/Node/Rust have defaults; `custom` or unknown yields none. | partially-wired |
| `persistent_storage` | Controls Docker-backed per-repo volume setup; no equivalent without container storage. | partially-wired |
| `max_retries` | Values greater than zero override global; zero or negative falls back to global. | production-wired |
| `allow_concurrent` | Root repo entries affect dispatcher. `allowConcurrentFor` does not inspect identity repo lists. | partially-wired |

### Container settings

| Key | Effective behavior |
| --- | --- |
| `enabled` | Requires non-empty `image`, `agent.mode = "nats"`, and non-empty `nats.url`. |
| `image` | Required only when enabled. |
| `max_concurrency` | Zero means no limit; used by general task dispatcher and container pool. |
| `max_uptime` | Default `60m` only when enabled; caps container lifetime. |
| `volume_ttl` | Default `72h` only when enabled; negative rejected. |
| `pull_policy` | Default `missing` when enabled. Pool code recognizes `missing` and `always`; other strings silently skip pre-pull. |
| `network` | Empty uses best-effort self-network detection. Repository overlay pins `archie-core_default`. |

### Channel settings

| Keys | Effective behavior |
| --- | --- |
| `chat.models` | Optional interactive catalog; empty falls back to workflow model references. |
| `chat.telegram.token_env` | Empty disables Telegram; non-empty names env variable; empty value is fatal. |
| `chat.telegram.allowed_user_ids` | Empty denies every sender; IDs match Telegram sender IDs, not chat IDs. |
| `chat.telegram.update_check_command`, `update_install_command` | Argv arrays; check enables update status; install further enables approved installation. |
| `chat.email.listen_addr`, `relay_addr` | Empty listen disables SMTP; relay configures outbound replies. |
| `chat.webhook_addr` | Empty disables; malformed host/port falls back inside `parseListenAddr`. |

Telegram `/restart` re-runs same TOML overlay load, then replaces only gateway
token and allowed-user list. Every other file setting is startup-only.

## Treat multi-identity as partially wired

`finalize` requires each identity's `name`, `bot_user`, non-empty repos, forge
type, and `SecretRef` token. `cmd/archied` builds an identity forge client and
worktree manager.

Do not infer complete isolation:

- `cmd/archied` resolves root forge client before all identity runners.
- Identity `name`, forge, repos, poll interval, bot user, bot email have
  production reads. Identity bot email not defaulted despite comment.
- Identity `models`, `providers`, `dispatch`, `budgets`, `notify`, and
  `diff_cap_lines` decode but workflows still receive root `Config`.
- Identity polling reads root `dispatch`, `label`, and `bot_user`.
- Container task snapshots and provider maps come from root config; task RPC
  servers are root-forge/root-worktree.
- Root-only concurrency lookup ignores identity repo `allow_concurrent`.

Classify multi-identity changes field by field. Never label aggregate
`IdentityConfig` production-wired.

## Preserve worker and secret boundaries

`Config.ForTask` creates a detached root snapshot: models, budgets, dispatch,
diff cap, notification, and forge host. `TaskConfig.ToConfig` reconstructs
only those fields. Forge tokens, provider values, NATS credential references,
repos, and infrastructure settings do not enter `TaskConfig`.

At top-level `[forge]`, prefer `token = {engine = "env", key = "NAME"}`. Legacy
`token_env = "NAME"` is converted only when structured reference is empty;
structured reference wins when both appear. This compatibility rule exists
because an earlier migration silently dropped deployed `token_env` values and
crash-looped the daemon. `finalize` does not apply that conversion inside each
identity: identity forges require explicit structured `token`.

| Source | Archied | Subprocess | Agent container / standalone worker |
| --- | --- | --- | --- |
| Forge `token = {engine="env", key="X"}` | Resolves `X`; empty is fatal. | Not forwarded. | Not forwarded; forge ops proxy to daemon. |
| Provider `api_key_env = "X"` | Runtime reads `X`. | Auto-allowlists selected provider's `X`. | Daemon forwards each present provider variable under original name. |
| `nats.token_env = "X"` | Reads `X`; empty fatal when configured. | Not agent subprocess setting. | Daemon maps to fixed `NATS_TOKEN`; worker also reads `NATS_URL`. |
| Telegram `token_env = "X"` | Reads `X`. | Not forwarded. | Not forwarded. |
| `agent.env = ["X"]` | Names validated. | Copies present `X` from daemon env. | Not used by container construction. |

`cmd/archied` creates `secret.NewRegistry`, which registers only the `env`
engine. Non-env `SecretRef` values are not production-wired.

Compose passes only `ARCHIE_GITEA_TOKEN`,
`ARCHIE_GITEA_TOKEN_ARCHIE`, `HEYARCHIE_TELEGRAM_BOT_TOKEN`, and
`DEEPSEEK_API_KEY` into `archied`.

## Follow the approved target

Current code puts infrastructure paths, domain policy, channel settings,
secrets, execution settings, repository associations, and extension data in one
mutable `config.Config`. Treat this as a current load-bearing weakness, not a
pattern to copy.

`docs/architecture/configuration.md` is approved as of 2026-07-28:

- move file/env/secret/overlay loading to private
  `internal/infrastructure/configuration` input boundary;
- translate inputs in `internal/app`;
- give each domain/plugin typed owned settings and validation;
- dissolve `internal/config` rather than rename the global model;
- split `Repo` and `IdentityConfig` by actual capability ownership.
