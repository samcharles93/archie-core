# Configuration

**Status:** Approved; existing-type assignment is ongoing  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Ownership

Configuration has two separate responsibilities:

1. `internal/infrastructure/configuration` loads files, environment variables,
   secret references, overlays, and compatibility representations.
2. Each domain and plugin owns the typed runtime settings required for its
   behaviour.

`internal/app` translates external input into those settings, validates
cross-capability combinations, and supplies each component only its own
settings.

The following are prohibited:

- passing complete application configuration into a domain;
- making a domain depend on TOML, YAML, environment-variable names, secret
  references, or a configuration library;
- defining a central runtime configuration type imported across the system;
- placing plugin options in an unrelated global configuration model.

## Private input document

`internal/infrastructure/configuration` MAY use a complete decoded document
while discovering files, decoding sections, and applying overlays. It is an
input-boundary DTO only.

The document:

- remains private to the configuration infrastructure;
- is composed from separately defined feature sections;
- is immediately translated by application composition;
- is never returned as the application's runtime model;
- is never passed through runtime components.

External DTOs and domain runtime settings are separate types.

Plugins supply external configuration definitions through a narrow extension
contract. The core loader MUST NOT gain a field or recognized filename for every
plugin.

Invalid configuration input MUST NOT replace active valid settings or terminate
unaffected capabilities. Configuration changes use the candidate validation,
promotion, audit, and recovery requirements in
[safe-change-and-recovery.md](safe-change-and-recovery.md).

## Dissolution of `internal/config`

`internal/config` has no target location. It MUST be dissolved and MUST NOT
survive as a shared package, configuration domain, or renamed global model.

Its loading behaviour moves to `internal/infrastructure/configuration`. Existing
types and methods are reassigned by behaviour. Repository execution policy,
dispatch policy, retry policy, ecosystem defaults, tool policy, memory settings,
indexing settings, provider settings, and channel settings move to their
respective owners.

## Repository settings

The existing `config.Repo` MUST NOT move intact into a repository domain or
shared settings package. A managed repository is an application association:

- work intake owns which repository references an identity monitors;
- task execution owns gates, preflight, test protection, ecosystem behaviour,
  and change limits;
- task lifecycle owns retry behaviour;
- scheduling owns concurrency policy;
- workspace management owns base branch and persistent-storage requirements.

Application composition correlates these settings by repository reference. No
domain receives a universal repository profile.

## Documentation

Generated configuration reference describes external sections and their owning
capabilities. It MUST NOT establish a global Go configuration structure as the
public contract.

## Value classification rule

Every value that controls behaviour MUST have an explicit owner and MUST be
classified as exactly one of:

1. **Invariant** — changing it changes the meaning of the program, a wire
   protocol, or a domain concept. It may be a Go constant.
2. **Runtime setting** — an operator, identity, domain, plugin, or deployment
   may legitimately choose a different value. It is typed configuration owned by
   the affected capability.
3. **Policy value** — it expresses what is allowed, limited, routed, retried,
   protected, or gated. Its typed definition belongs to the owning domain and it
   is evaluated through the policy system.
4. **Derived value** — it is computed from other settings and is not
   independently mutable.
5. **Runtime state** — it changes as the application operates, such as the
   active model for a conversation. It is changed by a domain command and is not
   configuration merely because it can be changed.

An unclassified behavioural literal is a defect.

`const` MUST NOT be used merely because a value currently has one default.
Constants are reserved for actual invariants: enum discriminants, protocol
versions, fixed compatibility tokens, and values whose alteration would change
the program's semantics rather than configure an installation.

In Go, a constant is a compile-time value and does not represent mutable runtime
storage. It generally has no address that can be observed or changed.
Architecturally, a constant therefore asserts that the value cannot change for
the lifetime of the application. Any value that may legitimately change while
the application is running, between application instances, or between
deployments is not a constant.

A runtime setting may have a default, but the default is not its only access
path. The owner exposes a typed settings structure, validation, and a
`DefaultSettings` constructor or equivalent. Private constants used to implement
defaults do not turn those defaults into invariants and MUST NOT be consumed
directly by runtime code.

Each runtime setting and policy value MUST declare:

- its owner;
- type, units, validation constraints, and safe bounds;
- default and derivation, if any;
- whether it is startup-only, live-reloadable, or restart-required;
- whether it may differ by installation, identity, repository, workflow,
  channel, or plugin instance;
- whether it is secret, sensitive, or safe to report;
- its current source, effective value, version, and last-known-good version;
- the command and identity required to change it.

## Current configuration migration ledger

This ledger records the current meanings hidden inside `internal/config`. It is
the source inventory for dissolving that package; rows are split where the
existing structure combines different owners.

| Current values                                                                                  | Current location      | Target owner and requirement                                                                                                                               |
| ----------------------------------------------------------------------------------------------- | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `WorkDir`, `DBPath`                                                                             | root `Config`         | Workspace/storage infrastructure settings. `DBPath` is the prefix for independent task and conversation SQLite files. Defaults derive from the platform data directory.                                                               |
| `SkillsDir`, `PluginDir`                                                                        | root `Config`         | Skill and plugin infrastructure settings, independently typed.                                                                                             |
| `PollInterval`                                                                                  | root `Config`         | Work-intake scheduling setting; not a daemon-wide constant.                                                                                                |
| `Label`, `BotUser`, `BotEmail`                                                                  | root `Config`         | Identity and dispatch associations. `BotEmail` may be derived from the selected forge account but remains overrideable.                                    |
| `MaxRetries`                                                                                    | root `Config`, `Repo` | Retry policy owned by the operation being retried. The duplicate global/repository representation must disappear.                                          |
| `DiffCapLines`                                                                                  | root `Config`         | Agent-change policy value.                                                                                                                                 |
| `MaxSteps`, `WallClock`, `GateMaxFailures`                                                      | `Budgets`             | Workflow-execution policy, with units and zero/unlimited semantics made explicit.                                                                          |
| `Class`, `APIKeyEnv`, `BaseURL`                                                                 | `Provider`            | Provider instance settings. Secret lookup becomes a `SecretRef`, not an environment variable name leaking into the provider domain.                        |
| `Mode`, `Command`, `Env`                                                                        | `LegacyAgent`         | Decode-only compatibility input for the removed executor selector. Delete after the config migration window; it has no runtime settings owner.             |
| `Type`, `Host`, `Token` / `TokenEnv` (older form)                                               | `Forge`               | Forge adapter settings. The older `TokenEnv` input is accepted only at the loader boundary.                                                                              |
| `Trigger`, `AckReaction`, label names                                                           | `Dispatch`            | Work-intake and forge presentation policy. Label vocabulary is configurable per forge/identity association.                                                |
| `Engine`                                                                                         | `MemoryConfig`        | Memory engine selection. `internal/domain/memory` is the only engine family; per-engine settings remain owned and validated by that engine.                |
| `Name`, `Transport`, `Command`, `Args`, `WorkDir`, `URL`                                        | `MCPServer`           | One typed MCP server instance specification. Transport-specific variants must reject fields belonging to other variants.                                   |
| `MaxResultChars`                                                                                | `ToolPolicy`          | Tool-execution policy values, not generic daemon configuration.                                                                                            |
| `URL`, `TokenEnv`                                                                               | `NATSConfig`          | NATS transport settings with a secret reference replacing the environment-name field.                                                                      |
| `Image`, `MaxConcurrency`, `MaxUptime`, `VolumeTTL`, `PullPolicy`, `Network`                    | `ContainerConfig`     | Mandatory managed-worker infrastructure settings and resource policy. The legacy `enabled` input is decode-only.                                           |
| model mappings, email, webhook, Telegram                                                        | `ChatConfig`          | Separate channel instance settings plus channel-routing associations; there is no chat-wide settings owner.                                                |
| `AllowedUserIDs`, `Token` / `TokenEnv` (older form), update commands                           | Telegram config       | Telegram adapter settings and access policy. `Token` is the preferred `secret.SecretRef`; `TokenEnv` remains a backwards-compatible fallback.               |
| listen/relay addresses                                                                          | email config          | Email adapter instance settings.                                                                                                                           |
| webhook target                                                                                  | `Notify`              | Notification adapter instance settings.                                                                                                                    |
| web listen address                                                                              | `Web`                 | Web transport settings.                                                                                                                                    |
| `listen` under `[services.state]` and `[services.gateway]`                                      | `ServiceConnection`   | The bind address of the process that owns the service. Read only by that process, and defaulted to the address its clients dial so the pair cannot drift.   |
| index directory and database path                                                               | `Indexing`            | Index infrastructure settings, derived from storage settings unless explicitly overridden.                                                                 |
| forge, dispatch, models, providers, containers, memory, tools, indexing and repositories        | `IdentityConfig`      | Existing composite duplicates unrelated settings. Replace it with associations from an `IdentityID` to independently owned configured instances.           |
| owner, name, base, gate, protection, ecosystem, preflight, storage, retry and concurrency       | `Repo`                | Split across work intake, workspace, workflow execution, policy, gate, and scheduling as described in Repository settings.                                 |
| `TaskConfig`, `TaskForge`                                                                       | worker snapshot       | Replace with the minimum immutable, versioned WorkflowExecution input assembled by the application. It is not a second configuration model.                |
| `Extra` and arbitrary maps                                                                      | root/provider config  | Allowed only at an extension boundary whose owning plugin/provider supplies schema and validation. Core configuration never consumes an untyped catch-all. |

## Behavioural literals and incorrectly fixed values

The following current values affect useful behaviour but are not represented as
owned typed settings or policy. They MUST be classified and migrated before the
old configuration package is removed. The listed target is the architectural
decision; exact setting names are implementation details.

| Capability                   | Values fixed in current code                                                                                                                                                          | Required classification                                                                                                                                                                 |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Chat agent                   | maximum steps `8` in `cmd/archied`                                                                                                                                                    | Workflow/chat execution policy.                                                                                                                                                         |
| Context compression          | enabled, threshold `0.5`, context size `128000`, protect first `3` and last `20`, compression marker, token estimate of four characters per token                                     | Model/context runtime settings. The marker is presentation; the estimate is provider/model strategy, not a universal truth.                                                             |
| Tool guardrails              | escalation thresholds `3`, `5`, `10`, `2`                                                                                                                                             | Tool-use policy with named meanings and bounded defaults.                                                                                                                               |
| Tool output                  | per-result cap `50000` characters, no aggregate turn cap, preview `500` characters                                                                                                    | Tool-execution resource and presentation settings. Per-result spill/truncation remains; turn continuation is not character- or token-gated.                                             |
| Pairing/access               | code TTL `1h`, maximum pending `3`, rate window `10m`, failures `5`, lockout `1h`, maximum lockout `24h`                                                                              | Access policy. Code length/alphabet and salt length require an explicit security-constraint decision; they are not automatically valid constants.                                       |
| NATS daemon consumer         | stream `ARCHIE_TASKS`, consumer `archie-daemon`, deduplication `2m`, poll `2s`, deliveries `3`, acknowledgement wait `5m`, inactive threshold `24h`, subjects                         | NATS topology settings plus delivery policy. Subject grammar or protocol headers may be invariants only if declared as versioned wire contracts.                                        |
| NATS worker consumer         | stream `ARCHIE_TASKS`, consumer `archie-agent`, deduplication `2m`, poll `5s`, acknowledgement wait `30m`, deliveries `3`, inactive threshold `24h`, queue group and retry delay `1s` | Worker transport settings and delivery/retry policy. Shared stream defaults must have one owner rather than two copies.                                                                 |
| Worker readiness             | readiness timeout `20s`, retry delay `250ms`                                                                                                                                          | Worker lifecycle and retry policy.                                                                                                                                                      |
| Execution/RPC                | agent execution `30m`, task-run RPC `60s`, worktree RPC `15m`                                                                                                                         | Per-operation timeout policy.                                                                                                                                                           |
| Indexing                     | build timeout `10m`, metadata timeout `5s`                                                                                                                                            | Index operation policy.                                                                                                                                                                 |
| Plugin/tool cleanup          | plugin rollback `10s`, tool-provider cleanup `10s`, MCP-provider cleanup `10s`                                                                                                        | Lifecycle shutdown/rollback policy owned by each engine, with a shared policy vocabulary where appropriate.                                                                             |
| Memory read path              | per-scope record limit `20`, rendered `<memory>` block byte cap `8192`                                                                                                                | Memory read policy owned by the engine family (`internal/domain/memory`).                                                                                                                |
| Built-in memory store        | per-file size limit `100KiB`, query result limit `100`                                                                                                                                | Split into memory resource policy, input policy, and presentation settings.                                                                                                              |
| Memory safety scanner        | compiled prompt-injection and sensitive-data patterns with fixed warn/block consequences                                                                                              | Versioned memory-domain policy definitions. Patterns and consequences must be inspectable and replaceable without editing scanner code.                                                 |
| MCP stdio                    | initial backoff `500ms`, maximum backoff `30s`, unlimited default retries, send and shutdown `5s`                                                                                     | MCP transport retry, request, and lifecycle policy. Unlimited retries must be explicit rather than a zero-value accident.                                                               |
| MCP SSE/HTTP                 | reconnect `1s` to `30s`, POST/request timeout `30s`                                                                                                                                   | MCP transport settings and retry policy.                                                                                                                                                |
| Message framing              | MCP frame `4MiB`, worker protocol frame `8MiB`, diagnostic `64KiB`, media/base64 `64MiB`                                                                                              | Resource/security policy unless a published wire protocol mandates the limit. Limits must be configurable only within safe bounds.                                                      |
| Webhooks and web UI          | webhook bind `0.0.0.0:8644`, webhook shutdown `5s`, web read-header `5s`, web shutdown `2s`                                                                                           | Transport instance and lifecycle settings.                                                                                                                                              |
| Telegram                     | typing refresh `4s`, draft interval `700ms`, update-action TTL `10m`                                                                                                                  | Telegram adapter presentation and lifecycle settings.                                                                                                                                   |
| Telegram limits              | message length `4000` and callback/update prefixes                                                                                                                                    | Record the upstream platform constraint and version it as an adapter compatibility limit; prefixes may remain wire constants when changing them would invalidate outstanding callbacks. |
| Containers                   | stop and pull timeouts `10s` and `30s`                                                                                                                                                | Container lifecycle policy.                                                                                                                                                             |
| HTTP clients                 | feasibility notification timeout `10s`; other paths use `http.DefaultClient`                                                                                                          | Adapter request policy. Every outbound client requires an explicit timeout profile.                                                                                                     |
| Workflow routing             | labels `bug`, `feature`, `bootstrap`; workflows `tdd`, `feasibility`, `bootstrap`; fallback `implement`                                                                               | Agent-system routing policy. Workflow identifiers are references, not compiled routing decisions.                                                                                       |
| Filesystem/deployment        | `.agents/skills`, `.archie/stages`, `.archie/gate.go`, `/data/worktree`, `/data/repo`, cache names/paths and prepared sentinel                                                        | Split into deployment settings, repository conventions, and genuine internal protocol markers. Paths an installation may relocate are settings.                                         |
| Packaged release data        | `/usr/share/archie/CHANGELOG.archied.md` and `/usr/share/archie/CHANGELOG.archie.md`                                                                                                  | Packaging/deployment settings or paths derived from an installation prefix.                                                                                                             |
| Forge presentation           | default label colour `bfd4f2` and state-label colours                                                                                                                                 | Forge adapter presentation settings.                                                                                                                                                    |
| Fallback system prompt       | compiled fallback prompt                                                                                                                                                              | Agent-system default content, versioned and replaceable through its owning settings; safety constraints remain policy/code.                                                             |
| Default paths and identities | config path, data paths, listen addresses, model roles and workflow names                                                                                                             | Typed defaults belonging to their respective owners, never global constants consumed throughout the application.                                                                        |

This inventory is intentionally broader than exported Go `const` declarations. A
literal hidden in a constructor, timeout, switch, retry loop, path join, prompt,
label map, or client setup restricts behaviour just as effectively as an
incorrectly used constant.

## Constants that may remain

A value may remain constant only when its owner documents why runtime variation
would be invalid. Typical valid cases are:

- domain enum values and state names;
- a versioned wire-protocol version or framing token;
- reserved internal headers and callback prefixes that peers must interpret
  identically;
- compile-time mathematical relationships;
- sentinel errors and internal type identifiers.

Names such as a stream, consumer, label, workflow, file, directory, network,
model role, timeout, retry count, capacity, size limit, colour, prompt, command,
or executable are not invariants merely because the current application has only
one of them.

Architecture tests SHOULD reject exported behavioural constants outside approved
invariant packages and SHOULD detect direct consumption of private default
constants outside their owning `DefaultSettings` construction.

## Current mutation and reload ledger

The application currently changes behavioural values through unrelated paths:

| Current change seam                  | Current behaviour                                                                                                                | Required target                                                                                                                                                                         |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Telegram `/restart` in `cmd/archied` | Re-reads the global document and directly replaces Telegram token and allowed-user IDs; failure retains old values.              | Submit a Telegram-settings candidate through the universal change protocol. Validate the complete candidate, promote atomically, audit the acting identity, and retain last-known-good. |
| Active model selection               | `chatModelManager.SetActiveModel` changes a model reference.                                                                     | A conversation/session command changing runtime state. Validate against configured model instances and audit it; do not rewrite configuration.                                          |
| Active provider selection            | `chatModelManager.SetActiveProvider` changes provider selection.                                                                 | A conversation/session command changing runtime state, with the same distinction from provider-instance configuration.                                                                  |
| Persona selection                    | `PersonaRegistry.SetActive` mutates an in-memory per-session choice.                                                             | Agent-system session state. Persistence and audit requirements are owned by that domain, not the configuration loader.                                                                  |
| File and environment overlays        | Applied while loading `internal/config`; defaults are filled by mutation in `finalize`.                                          | Configuration-infrastructure input processing that produces separately typed candidates. Defaults and derivations remain visible in provenance.                                         |
| SIGHUP config reload (2026-08)        | Re-resolves the file config, re-applies the database layer over it, validates, then atomically republishes through `config.Holder`. A failed reload, whether the file or the control plane caused it, keeps the running config and records `last_error`/`last_error_at` in `/api/config`. | Candidate promotion with last-known-good retention: the running snapshot is untouched on failure. A bounded observation period and actor audit are not yet implemented. |
| Control-plane resource command (2026-09) | `ControlPlaneService.Command` validates a whole resource document against its owning feature, then writes it with an audit record (actor, source, request ID, expected version) under optimistic concurrency. Hosted on the State Store's gRPC server; the Web UI and messaging channels are its only clients. See `docs/prds/runtime-control-plane.md`. | Met for validate-stage-promote, actor audit and versioned history. Per-field health observation and validate-before-switch on live apply are not yet implemented. |

Any further mutation seam discovered during migration MUST be added here before
it is changed. Direct field assignment on a live settings object is prohibited.

## Universal change protocol

Changing configuration is an application command performed by an identity. It
MUST:

1. resolve the target owner and current version;
2. parse into that owner's typed settings;
3. validate syntax, semantic constraints, policy, references, and safe bounds;
4. stage a candidate without altering the active value;
5. run owner-defined readiness and health checks;
6. atomically promote the candidate and record actor, source, reason, diff,
   prior version, and new version;
7. observe the affected capability for a bounded period;
8. retain the new version on success, or automatically restore last-known-good
   on failure;
9. report the rejected or rolled-back candidate without terminating unaffected
   capabilities.

Startup loading uses the same candidate rules. A bad user value therefore does
not partially mutate the application or make a previously healthy capability
unavailable.

Settings that cannot safely reload in-process are marked restart-required. The
change may still be validated and versioned immediately, but promotion occurs
through a controlled restart with the same health and rollback semantics.

## Implemented runtime reload and settings writes (2026-09)

Two mechanisms change behavioural values at runtime: SIGHUP re-reads the file
config, and the control plane owns every database-backed setting. The dashboard
runtime overlay that used to sit between them was deleted once the control plane
covered its surface; `-config-overlay` names a file overlay and is unrelated.

### Published snapshot: `config.Holder`

The running configuration is a published snapshot held in `config.Holder`
(`internal/config/holder.go`). Get returns a value under RLock; Set replaces the
whole snapshot. A published Config is immutable: reload constructs a fresh one
and calls Set. `config.Clone` deep-copies every reference-type field so a failed
decode cannot mutate the published snapshot's shared maps. `Holder.Get` panics
on a nil receiver — a forgotten `NewHolder` must fail loudly, not boot a daemon
that silently runs on an empty config.

### The reloadable-field criterion

The diff-and-warn on reload asks whether a changed field takes effect without a
restart. The criterion is: **a field is reloadable when every consumer re-reads
it.** That question read as "what does the daemon re-read" while the daemon owned
the only Holder; it stopped being sufficient when webui began sharing that
Holder, because webui handlers re-read on every request. The allowlist lives in
`cmd/archied/reload.go` and is deliberately a deny-by-default list: a field added
to Config later defaults to requires-restart, forcing whoever adds it to decide
deliberately. A reload that changes any non-allowlisted field logs
"requires a restart" with the field names; a fully-applied reload logs Info.

### SIGHUP reload

SIGHUP re-resolves the file config, re-applies the database layer over it
(`boot.runtimeConfig`, the same call boot makes), validates, and atomically
republishes through the shared Holder. A failed reload keeps the running config
and records `last_error`/`last_error_at` in `/api/config` so the stale state is
visible where the operator is looking. The three poll tickers re-read
`PollInterval` per iteration and reset on change, so a reloaded interval
genuinely takes effect.

### Settings writes go through the control plane

Database-backed settings are read and written through
`ControlPlaneService` on the State Store's gRPC server: catalog, query,
command, watch. Each feature owns its resource's validation and commands;
storage writes versions and audit records only. `docs/prds/runtime-control-plane.md`
is the specification.

Two properties matter to anyone changing this area:

- **Bootstrap stays file-owned.** The daemon reads the file config first and
  layers database resources over it at boot (`boot.loadRuntimeConfig`). Values
  the daemon needs before it can reach the State Store — the database path,
  listen addresses, credentials — stay file-only by design.
- **Reload re-reads them.** SIGHUP resolves the file document and then runs the
  same `boot.runtimeConfig` layering boot does, so a reload cannot revert a
  database-owned setting to its file value. A control plane that cannot answer
  fails the reload rather than publishing the file's values over the running
  ones. Workflow execution budgets arrive on a watch rather than in a resource
  query, so boot records the last published settings and the reload re-applies
  them from there.

## Completion criteria

The configuration rearchitecture is complete only when:

- `internal/config` no longer exists;
- every behavioural value has one classification and owner;
- every current field and hard-coded value in the ledgers has a recorded
  disposition;
- domains and plugins receive only their typed settings;
- all changes use semantic commands and the universal change protocol;
- active, candidate, rejected, rolled-back, and last-known-good versions are
  queryable with secret-safe provenance;
- generated documentation lists every external key, default, constraint,
  mutability class, owner, and deprecation;
- architecture tests prevent a new central runtime configuration model, direct
  live-setting mutation, and incorrectly fixed behavioural values.

## Startup policy: missing credential degrades, invalid config is fatal

Decided 2026-07-30. Recovered 2026-08-09 from the pre-migration issue tracker.

A missing **credential** disables a capability. Only an **invalid config** stops
the daemon. "Forge is a feature, not a requirement."

Before `518a1a0`, an unresolvable forge token did `return 1` in
`cmd/archied/main.go`, so a chat-only user who never configured GitHub got no
chat, no gateway, nothing — and under a systemd unit with `Restart=on-failure`
and no start limit it crash-looped every five seconds with the error scrolling
past unread. Forge construction now lives in `resolveForge`, shared by the
primary forge and every configured identity, and degrades to `forge.NewNoop`
with a warning. The identity path had the same defect and was worse: one
identity's missing credential killed all the others.

Malformed configuration still fails fast in `configuration.Validate`, well
before this point. That distinction is the whole design.

Apply the same reasoning to any future capability credential, LLM keys
especially: start, report the capability as unavailable, let the operator see it.
Guarded by `TestResolveForgeDegradesInsteadOfFailing`.

**Known consequence, tracked separately:** `worktree.Manager` still receives the
empty token, so pushes fail late rather than at startup.

## Config generation belongs to `archied setup`, not `install.sh`

Decided 2026-07-30 ([#368](https://github.com/samcharles93/archie-core/issues/368)).
Recovered 2026-08-09 from the pre-migration issue tracker.

`install.sh` must stop generating `config.toml`; `archied setup` owns it. The
installer hand-rolling TOML has already drifted from the schema once: the
generated-config branch hardcoded `key = ARCHIE_GITHUB_TOKEN` and
`host = ${GITEA_URL}` regardless of the answers given, so choosing Gitea produced
a config naming a token the installer never wrote (archied then exited 1), and
choosing GitHub pointed `host` at `gitea.example.com`. `61a5b35` patched this
with a `forge_block` shell helper shared by both config paths — that helper is a
**workaround**, deleted by
[#72](https://github.com/samcharles93/archie-core/issues/72). The durable fix is
that the code writing the config is the code that reads it.

Why Go and not Python: the installer must run where nothing is installed yet, and
`python3` with `tomllib` means 3.11+, which is not safe on LTS distros. `archied`
is already being built two steps earlier.

**Key constraint for the port:** `BurntSushi/toml` does **not** survive a
decode/encode round trip with comments intact, and `config.example.toml` carries
documentation users are meant to hand-edit. Decide the write strategy
([#412](https://github.com/samcharles93/archie-core/issues/412)) before writing
any prompting code. Do not port a separate `config.yaml`/`auth.yaml` secret
split — Archie already has `secret.SecretRef{engine,key}`, so setup writes the
reference to TOML and the value to the secret engine.
