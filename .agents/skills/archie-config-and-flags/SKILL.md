---
name: archie-config-and-flags
description: Trace, add, rename, deprecate, or debug Archie configuration and command-line flags from external input through defaults, validation, overlays, environment and secret handling, worker snapshots, deployment passthrough, and the production consumer. Use for internal/config, config.example.toml, config.docker.toml, docker-compose.yml, archied or archie-agent flags, multi-identity settings, mode-parity questions, and reports of a setting that parses but has no effect.
---

# Trace Archie configuration and flags

Treat configuration as a dataflow, not as a struct inventory. Prove every
claimed setting has an input, normalization path, composition seam, and runtime
consumer.

Current evidence date: **2026-07-28**. Re-verify volatile defaults, deployment
wiring, and status labels before changing behavior.

## Route work to the right skill

Do not use this skill to:

- choose a new architecture or owner; use
  `archie-architecture-planning-campaign` and `archie-architecture-contract`;
- discover an unknown feature's whole blast radius; use
  `archie-codebase-discovery`, then return here for its configuration seam;
- inspect live hosts, rotate secrets, deploy, or recover an instance; use
  `archie-run-and-operate`;
- define NellDB settings or persistence semantics; use `archie-nelldb`;
- approve a behavioral change or claim acceptance; use
  `archie-change-control` and `archie-validation-and-qa`.

Use these status terms exactly:

| Status | Meaning |
| --- | --- |
| **production-wired** | A real entrypoint decodes or accepts the value and a runtime consumer uses it. |
| **partially-wired** | Some modes, scopes, or fields consume it, but parity is incomplete. |
| **test/library-only** | An API and tests exist, but no current binary selects it. |
| **decoded-but-unwired** | Decoding/defaulting succeeds, but no production consumer uses the effective value. |
| **experimental/candidate** | Code exists, but operational correctness or the public contract is not established. |
| **external-production-unknown** | The repository does not contain the deployed effective value. |

Never upgrade a status merely because decoding tests pass.

## Trace one setting before editing it

Perform this sequence:

1. Find every external spelling, Go field, flag, environment name, example, and
   deployment reference.
2. Identify the selected loader or flag set. Do not assume every exported loader
   is used.
3. Read `finalize` and any consumer-side defaults. Record zero, empty, negative,
   and sentinel semantics separately.
4. Follow copies across `ForTask`, `ToConfig`, NATS messages, subprocess
   environment selection, container environment construction, and identity
   overlays.
5. Find the production composition site and final behavior-changing read.
6. Find a test for decoding, defaulting, invalid input, production composition,
   and mode parity. Record missing layers as gaps.
7. Check `config.example.toml`, `config.docker.toml`, `docker-compose.yml`, and
   deployment environment passthrough.
8. Assign one status from the table above and cite the exact evidence paths in
   the change record.

Start with:

```bash
rg -n 'LoadOverlay|LoadDir|ForTask|ToConfig|flag\.' \
  internal/config cmd/archied cmd/archie-agent
rg -n 'FIELD_NAME|external_key|ENV_NAME' \
  --glob '*.go' --glob '*.toml' --glob '*.yml' --glob '*.yaml' .
```

Do not search only `internal/config`: a field with no reads outside that package
is usually not production-wired.

## Select the real input path

| Input path | Current behavior | Status |
| --- | --- | --- |
| `archied -config PATH` | Defaults to `$XDG_CONFIG_HOME/archie/config.toml`, or `$HOME/.config/archie/config.toml`; `cmd/archied/main.go` calls `config.LoadOverlay`. | production-wired |
| `archied -config-overlay PATH` | Decodes base TOML, then overlay TOML into the same `Config`, then calls `finalize` once. Omitted overlay fields retain base values. | production-wired |
| `config.Load(PATH)` | Loads one TOML and finalizes it; no current binary calls it directly. | test/library-only |
| `config.LoadDir(base, overlay)` | Supports main YAML/TOML plus feature YAML and `conf.d`; no current entrypoint calls it. | test/library-only |
| `config.example.toml` | Load-tested example, not a deployed configuration. | test/library-only |
| `config.docker.toml` | Repository overlay mounted by Compose; it still depends on the host base config. | production-wired |
| Host base config and environment on `carina` | Mounted from `${HOME}/.config/archie/config.toml`; contents are absent from this repository. | external-production-unknown |

`LoadDir` recognizes `gateway`, `tools`, `memory`, `models`, and `identities`
feature YAML. It rejects unknown `config.<feature>.yaml` names, but accepts
unknown `conf.d/*.yaml` into `Config.Extra`. YAML takes precedence over TOML
when both are present in one directory. None of this changes the current daemon
path: `archied` selects TOML `LoadOverlay`, including during Telegram reload.

Both TOML and YAML decoding are non-strict at the field level: the loaders do
not reject every unknown key. Add explicit compatibility and typo tests; never
use “the file loaded” as evidence that a key took effect.

## Inventory CLI flags

### Public binaries

| Binary | Flag | Default and effect |
| --- | --- | --- |
| `archied` | `-config PATH` | Computed config path described above. |
| `archied` | `-config-overlay PATH` | Empty; overlay only fields explicitly present in the second TOML. |
| `archied` | `-once` | `false`; run one poll/process cycle and exit. |
| `archied` | `-requeue ID` | `0`; values greater than zero requeue one parked/waiting task, then exit unless `-once` is also set. |
| `archie-agent` | `-nats-url URL` | Empty; takes precedence over `NATS_URL`. Missing both is fatal. |
| `archie-agent` | `-consumer NAME` | `archie-agent`; names the JetStream durable consumer. |
| both | `-quickchecks N` | Incidental flag registered by `github.com/traefik/yaegi/stdlib` importing `testing/quick`; default `100`. Archie has no intentional consumer. Treat it as an unowned candidate defect, not supported configuration. |

The standalone agent always reads its token from `NATS_TOKEN`; there is no
token flag.

### Internal codesearch helper

`archied workspace-codesearch` bypasses daemon configuration before normal flag
parsing. It is an internal child-process contract:

| Subcommand | Required flags | Optional flags |
| --- | --- | --- |
| `build` | `--root`, `--index` | none |
| `candidates` | `--index`, `--pattern` | `--literal`, `--case-sensitive` |

Do not expose these helper flags as daemon settings without an architecture and
compatibility decision.

## Read the current wiring matrix

### Root daemon and capability settings

| Keys | Default, validation, and consumer | Status |
| --- | --- | --- |
| `work_dir`, `db_path` | Derive from `$XDG_DATA_HOME/archie`, else `$HOME/.local/share/archie`; `work_dir` drives worktrees, memory, derived index paths, and state files, while `db_path` opens NellDB. Literal `~` in an explicit value is not expanded by the loader. | production-wired |
| `skills_dir` | Empty falls back to `work_dir` for startup workflow registry. The `skill_activate` catalog still reads `work_dir`, and a container rebuilds from its mounted worktree. | partially-wired |
| `plugin_dir` | Empty disables daemon plugin loading; a configured directory is loaded in `cmd/archied`. | production-wired |
| `poll_interval` | Defaults to `60s`; root poll loop uses it and identities may override only their interval. | production-wired |
| `label` | No default or validation; used for `dispatch.trigger = "label"` or `"either"`. Empty therefore remains an accepted but likely ineffective pickup label. | production-wired |
| `max_retries` | Defaults to `3`; a repository value greater than zero overrides it. | production-wired |
| `bot_user`, `bot_email` | Single-identity mode requires `bot_user`; email defaults by forge type. `bot_user` drives assignment polling, store actor, sessions, and release state; both values form the Git signature. | production-wired |
| `forge.type`, `host`, `token` | Type defaults `github`, host defaults `https://github.com`; only `github` and `gitea` validate. The root token resolves before daemon construction and must be non-empty. | production-wired |
| `dispatch.trigger` | Defaults `assignee`; validates `assignee`, `label`, or `either`. | production-wired |
| `dispatch.ack_reaction` | Defaults `eyes`; case-insensitive `off` normalizes to empty. | production-wired |
| `dispatch.labels.*` | Missing `queued`, `working`, `waiting`, `pr`, `parked`, or `dead` keys default independently to `agent:*`. Used for forge state presentation. | production-wired |
| `models.<role>` | Workflow stages read a requested role then fall back to `builder`; triage reply judging falls back from `triage` to `planner`. References are not validated at load time. | production-wired |
| `providers.<name>.class`, `api_key_env`, `base_url` | Builds the ai-sdk catalog. `base_url` rejects parse errors, userinfo, query, and fragment; class/key/model compatibility is deferred. | production-wired |
| `agent.mode` | Defaults to `nats` when containers are enabled, otherwise `inprocess`; validates `inprocess`, `subprocess`, or `nats`. Subprocess caveat follows this table. | production-wired |
| `agent.command`, `agent.env` | Command defaults `archie-agent`; environment entries must be nonblank names without `=` and apply only to subprocess mode. | partially-wired |
| `budgets.max_steps`, `max_tokens`, `wall_clock`, `gate_max_failures` | No loader defaults; `Budgets` documents zero as disabling the limit. Workflow agent requests and gates consume them. | production-wired |
| `diff_cap_lines` | Zero is replaced by `400` in `finalize`; `StageDiffCap` treats values `<= 0` as disabled. Thus TOML zero cannot disable the cap, while an unvalidated negative value does. Do not use negative as a hidden operator contract. | production-wired |
| `web.listen` | Empty becomes `127.0.0.1:8484`; literal `off` disables. The dashboard has no authentication. | production-wired |
| `notify.webhook` | Empty disables; feasibility workflow POSTs human-decision notifications when set. Included in worker `TaskConfig`. | production-wired |
| `nats.url`, `token_env` | Empty URL keeps the SQLite path. A configured token variable must be non-empty. NATS enables distributed queue/RPC composition. | production-wired |
| `containers.*` | See the dedicated table below. Some values also control scheduling/storage outside the pool, demonstrating current cross-concern ownership. | production-wired |
| `chat.*` | See the channel table below. Empty channel addresses/token names disable those channels. | production-wired |
| `memory.provider`, `provider_config`, `session_ttl` | Feature YAML decodes and tests these fields, but `cmd/archied` always starts the built-in file provider at `work_dir/memory` and never reads them. | decoded-but-unwired |
| `tools.mcp_servers` | `cmd/archied` registers daemon-side MCP providers. Empty transport becomes `stdio`; only `stdio` is accepted; name and command are required. Invalid entries warn and skip. `url` is decoded but unused. Worker parity is not established. | partially-wired |
| `tools.tool_policy.*` | Feature YAML decodes `max_result_chars` and `parallel_execution`; no production read exists. | decoded-but-unwired |
| `indexing.index_dir`, `indexing.db_path` | `finalize` derives paths under `work_dir`; no entrypoint constructs `internal/indexing.Manager` from them. The internal helper alone does not complete wiring. | decoded-but-unwired |
| `extra` / unknown `conf.d` values | Test/library-only `LoadDir` stores untyped data, but core production has no consumer and the approved target forbids a core catch-all. | decoded-but-unwired |

The default `agent.command = "archie-agent"` is not verified subprocess
operation. `SubprocessRunner` expects one JSON invocation on stdin and one JSON
response on stdout; `cmd/archie-agent` is a long-running NATS worker that parses
NATS flags. Treat subprocess mode with that command as an open incompatibility
until an end-to-end test proves otherwise.

### Repository settings

| Keys | Effective behavior | Status |
| --- | --- | --- |
| `owner`, `name` | Required for every declared repo. A single-identity config with zero repos currently loads but polls nothing. | production-wired |
| `base` | `BaseBranch()` maps empty to `main`, but `daemon.process` pre-clones with raw `repo.Base`. Set it explicitly: the advertised empty default is not honored on that production path. | partially-wired |
| `gate` | Argv arrays run in order. Empty entries are skipped. TDD inverts the last non-empty command as the repro test by convention. No loader validates command meaning. | production-wired |
| `protect` | Path suffixes are enforced by the agent runner; this is not a prompt-only rule. | production-wired |
| `ecosystem`, `preflight`, `test_glob` | Resolved preflight/test-glob logic treats empty ecosystem as Go. Container storage receives the raw ecosystem, so empty gets no Go cache mounts. Go/Python/Node/Rust have defaults; `custom` or an unknown name yields none. Explicit test globs validate with `filepath.Match`. | partially-wired |
| `persistent_storage` | Controls Docker-backed per-repo volume setup; it has no equivalent effect without container storage. | partially-wired |
| `max_retries` | Values greater than zero override global retry count; zero or negative falls back to global. | production-wired |
| `allow_concurrent` | Root repo entries affect the dispatcher. `allowConcurrentFor` does not inspect identity repo lists. | partially-wired |

### Container settings

| Key | Effective behavior |
| --- | --- |
| `enabled` | Requires non-empty `image`, `agent.mode = "nats"`, and non-empty `nats.url`. |
| `image` | Required only when enabled. |
| `max_concurrency` | Zero means no limit; used by the general task dispatcher and again by the container pool. |
| `max_uptime` | Defaults to `60m` only when enabled; caps a container lifetime. |
| `volume_ttl` | Defaults to `72h` only when enabled; negative is rejected; governs persistent-volume cleanup. |
| `pull_policy` | Defaults to `missing` when enabled. Pool code recognizes `missing` and `always`; other strings are not rejected and silently skip pre-pull. |
| `network` | Empty uses best-effort self-network detection. The repository overlay pins `archie-core_default`. |

### Channel settings

| Keys | Effective behavior |
| --- | --- |
| `chat.models` | Optional interactive catalog; empty falls back to distinct top-level workflow model references. |
| `chat.telegram.token_env` | Empty disables Telegram; non-empty names an environment variable and an empty value is fatal. |
| `chat.telegram.allowed_user_ids` | Empty denies every sender; IDs match Telegram sender IDs, not chat IDs. |
| `chat.telegram.update_check_command`, `update_install_command` | Argv arrays; check enables update status, and install additionally enables approved installation. |
| `chat.email.listen_addr`, `relay_addr` | Empty listen disables SMTP; relay configures outbound replies. |
| `chat.webhook_addr` | Empty disables; malformed host/port input falls back inside `parseListenAddr` rather than failing config validation. |

Telegram `/restart` re-runs the same TOML overlay load, then replaces only the
gateway token and allowed-user list. Every other file setting is startup-only
in current composition. Active chat model/provider and persona selection are
runtime state, not configuration-file reload.

## Treat multi-identity as partially wired

`finalize` requires each identity's `name`, `bot_user`, non-empty repos, forge
type, and `SecretRef` token. It validates repository identity and test globs,
and `cmd/archied` builds an identity forge client and worktree manager.

Do not infer complete isolation:

- `cmd/archied` still resolves and constructs the root forge client before all
  identity runners, so a config accepted without root fields can still fail in
  production composition.
- Identity `name`, forge, repos, poll interval, bot user, and bot email have
  some production reads. Identity bot email is not defaulted despite its
  comment promising a fallback.
- Identity `models`, `providers`, `dispatch`, `budgets`, `notify`, and
  `diff_cap_lines` decode but workflows still receive root `Config`.
- Identity polling reads root `dispatch`, `label`, and `bot_user`.
- Container task snapshots and provider maps come from root config; task RPC
  servers are root-forge/root-worktree; container identity isolation is
  explicitly incomplete in `internal/daemon/daemon.go`.
- Root-only concurrency lookup ignores identity repo `allow_concurrent`.

Classify multi-identity changes field by field. Never label the aggregate
`IdentityConfig` production-wired.

## Preserve worker and secret boundaries

`Config.ForTask` creates a detached root snapshot containing only models,
budgets, dispatch, diff cap, notification, and forge host. `TaskConfig.ToConfig`
reconstructs only those fields. Provider descriptions travel separately in the
task-run request; forge tokens, provider values, NATS credential references,
repos, and infrastructure settings do not enter `TaskConfig`.

At the top-level `[forge]`, prefer
`token = {engine = "env", key = "NAME"}`. Legacy `token_env = "NAME"` is
converted only when the structured reference is empty; the structured
reference wins when both appear. This compatibility rule exists because an
earlier migration silently dropped deployed `token_env` values and crash-looped
the daemon. `finalize` does not apply that conversion inside each identity:
identity forges require an explicit structured `token`.

Use this environment matrix:

| Source | Archied | Subprocess | Agent container / standalone worker |
| --- | --- | --- | --- |
| Forge `token = {engine="env", key="X"}` | Resolves `X`; empty is fatal. | Not forwarded unless explicitly abused through `agent.env`. | Not forwarded; forge operations proxy to daemon. |
| Provider `api_key_env = "X"` | Runtime reads `X`. | Automatically allowlists the selected provider's `X`. | Daemon forwards each present provider variable under its original name. |
| `nats.token_env = "X"` | Reads `X`; empty is fatal when configured. | Not an agent subprocess setting. | Daemon maps its value to fixed `NATS_TOKEN`; worker also reads `NATS_URL`. |
| Telegram `token_env = "X"` | Reads `X`. | Not forwarded. | Not forwarded. |
| `agent.env = ["X"]` | Names are validated. | Copies present `X` from daemon environment. | Not used by container construction. |

`cmd/archied` creates `secret.NewRegistry`, which registers only the `env`
engine, and does not call `Registry.LoadDir`. Although plugin loading APIs and
comments describe custom secret engines, non-env `SecretRef` values are not
production-wired at token-resolution time.

Compose passes only `ARCHIE_GITEA_TOKEN`,
`ARCHIE_GITEA_TOKEN_ARCHIE`, `HEYARCHIE_TELEGRAM_BOT_TOKEN`, and
`DEEPSEEK_API_KEY` into `archied`. A config reference to any other environment
name requires an explicit deployment passthrough change. The live base config
is external, so do not assert which of these names it uses.

## Add, rename, or remove an axis

Use this checklist:

- [ ] Classify the value as invariant, runtime setting, policy, derived value,
      or runtime state.
- [ ] Name one target capability owner and its scope: installation, identity,
      repository, workflow, channel, or plugin instance.
- [ ] Prefer capability-owned settings. If a temporary `internal/config` field
      is unavoidable, record its target owner and deletion gate.
- [ ] Add a failing decoder test for the production TOML path.
- [ ] Add failing tests for default, explicit value, zero/empty/negative input,
      invalid syntax, semantic combination, and overlay inheritance.
- [ ] Add a production-composition test that observes the final consumer; a
      struct equality test is insufficient.
- [ ] Test single identity, multiple identities, and every affected execution
      mode separately.
- [ ] Update `ForTask`/`ToConfig` only when the worker needs the value; prove
      maps are detached and secret values/references are absent.
- [ ] Trace in-process, subprocess allowlisting, NATS payload, container
      environment, and RPC ownership. Fence unsupported modes explicitly.
- [ ] Update `config.example.toml`, `config.docker.toml`, Compose environment
      passthrough, and generated/reference docs only where applicable.
- [ ] For a rename, decode old and new spellings at the loader boundary, define
      precedence, and test old-only, new-only, and both. The `forge.token_env`
      crash-loop regression is the minimum compatibility standard.
- [ ] For removal, prove no production consumer, external spelling, overlay,
      worker copy, deploy reference, or supported old config remains. Define a
      dated deprecation and deletion gate; do not leave a permanent alias.
- [ ] Route behavior-changing acceptance through change control and validation.

Do not add a field that merely parses. Do not add arbitrary plugin fields to the
global struct. Do not expose secret values in config dumps, logs, task payloads,
or review evidence.

## Follow the approved target, not the current sprawl

Current code puts infrastructure paths, domain policy, channel settings,
secrets, execution settings, repository associations, and extension data in one
mutable `config.Config`. It passes that aggregate into `daemon.Daemon`,
`workflow.TaskContext`, and a second worker DTO. Treat this as a current
load-bearing weakness, not a pattern to copy.

`docs/prds/architecture/configuration.md` is approved as of 2026-07-28:

- move file/env/secret/overlay loading to a private
  `internal/infrastructure/configuration` input boundary;
- translate inputs in `internal/app`;
- give each domain/plugin typed owned settings and validation;
- dissolve `internal/config` rather than rename the global model;
- split `Repo` and `IdentityConfig` by actual capability ownership;
- stage, validate, health-check, promote, audit, and roll back configuration
  candidates without replacing last-known-good settings.

Use `archie-architecture-planning-campaign` before moving ownership, and use
`archie-change-control` for every behavior-changing migration slice. Keep each
slice small enough to deliver a visible success while deleting one obsolete
path.

## Provenance and maintenance

Re-verify declarations and defaults: `rg -n 'type (Config|IdentityConfig|Repo|Agent|ContainerConfig|ChatConfig|ToolsConfig)|func (Load|LoadOverlay|LoadDir|finalize|\(c Config\) ForTask|\(tc TaskConfig\) ToConfig)' internal/config`

Re-verify entrypoint and flag selection: `rg -n 'flag\.|LoadOverlay|LoadDir|ForTask|ToConfig|containerEnv' cmd/archied cmd/archie-agent internal/daemon`

Re-verify all exposed help: `env GOTMPDIR=/tmp GOCACHE=/tmp/archie-config-help-gocache go run ./cmd/archied -h`

Re-verify worker help: `env GOTMPDIR=/tmp GOCACHE=/tmp/archie-config-help-gocache go run ./cmd/archie-agent -h`

Re-verify config behavior: `env GOTMPDIR=/tmp GOCACHE=/tmp/archie-config-test-gocache go test ./internal/config -count=1`

Re-verify deployment passthrough: `rg -n 'command:|environment:|config\.toml|config\.docker\.toml|NATS_|TOKEN|API_KEY' docker-compose.yml config.docker.toml config.example.toml Dockerfile Dockerfile.archied`

Re-verify target doctrine: `rg -n 'Ownership|Dissolution|Current configuration migration ledger|Universal change protocol|Completion criteria' docs/prds/architecture/configuration.md`
