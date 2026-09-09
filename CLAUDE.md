# AGENTS.md

Guidance for any coding agent working in this repository.

Keep every rule here harness-agnostic: name the required behaviour, not the tool
that provides it (e.g. "spawn a fresh reviewer that did not write the code", not
a specific sub-command).

## Code Quality & Refactoring Standards

- **Zero Patch Stacking:** Never apply more than two sequential fixes to the
  same logic block. If a solution fails twice, scrap the block and rewrite it
  cleanly.
- **Root-Cause Fixes:** Fix invalid state at the producer, not via defensive
  checks at the consumer.
- **Architectural Simplicity:** Prefer a complete 20-line rewrite over a 5-line
  band-aid that adds conditional complexity or obscures intent.
- **Revert on Flail:** If an implementation becomes convoluted to satisfy edge
  cases, discard the approach and select a simpler design.

## What this is

**archied** is a resident daemon that polls a forge (GitHub, Gitea) for issues
assigned/labelled to it, works each one in an isolated git worktree through a
routed workflow (bootstrap / implement / TDD / feasibility), and opens pull
requests for human review.

See `ARCHITECTURE.md` for the package map, workflow engine design, task
lifecycle, and key design decisions. Read it before making non-trivial changes.
Key invariants: env-enforced gates, model never runs git, and agent execution
functions as a strict data boundary. Before adding a new plugin engine, satisfy
`ARCHITECTURE.md#plugin-engine-rule-strict`.

## Scope Discipline

`docs/architecture/` is authoritative for settled design.
`docs/architecture/migration-decisions.md` is the open decision register for the
domain migration.

- **Settled design exists** → Implement it immediately with a diff. Do not
  produce design prose. If a small detail is missing, ask the maintainer
  directly.
- **No settled design exists (or open in `migration-decisions.md`)** → Write a
  decisive, 1-page design doc before coding. Land new capabilities in
  `docs/prds/` and migration questions in
  `docs/architecture/migration-decisions.md`. Promote to `docs/architecture/`
  only after implementation lands.
- **Strictly Prohibited:** Ownership ledgers, field-level inventories,
  current-state traces, parity matrices for their own sake, and
  planning/refactor tracking issues.
- **Solo Project Context:** Prefer the smallest workable change over extensive
  defensive scaffolding.

## Deployment Model

- **Systemd is optional:** `deployments/` holds supported profiles
  (`single-forge-github.toml`, `multi-forge-github-gitea.toml`,
  `local-ollama-standalone.toml`, `docker-nats-stack.toml`,
  `systemd-user-service.md`). Never assume a systemd unit exists.
- **Chat filesystem confinement:** Builtin tools default to workspace jail via
  `SetPathConfinement(!unrestricted)`. Unrestricted access requires
  `[chat] unrestricted_filesystem = true`.
- **NATS endpoint immutability:** The daemon passes agent containers its startup
  connection URL (`d.ConnectedNATS.URL`). Containers and daemon must resolve
  this address. Embedded mode binds to the Docker bridge gateway. A SIGHUP
  reload of `nats.url` logs an error requiring restart to prevent split-brain
  delivery.
- **Container pull policy:** `containers.pull_policy` must be `"missing"`
  (default) or `"always"`. Private registries return 401 on `"always"` because
  no auth is sent. Refresh explicitly via `docker compose pull agent`.
- **Docker Compose:** `docker-compose.yml` carries optional external NATS and
  the `agent` build stanza. The agent uses a `build` profile so `up -d nats`
  never starts it as a persistent service.
- **Config path:** Runtime configuration lives at
  `${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`. Nothing in the working
  tree is load-bearing at runtime.

## Release Process

See `RELEASING.md`. Most sessions touch only `archied`, making releases
gateway-only (`RUNTIME=skip`) by default. Skip untouched components
automatically and note it in the handoff.

## Build & Test

Commands are defined in `Taskfile.yml` (requires Go 1.26.5,
[Task](https://taskfile.dev), `gofumpt`, `golangci-lint`, and Node/npm).

```bash
task build      # build archied and archie-agent binaries into bin/
task test       # go test ./... -count=1
task test:ui    # dashboard node tests (DOM-building primitives)
task ui         # build dashboard into ui/dist (LAW asset)
task fmt        # gofumpt -w . && go fix ./...
task vet        # go vet ./...
task lint       # golangci-lint run ./...
task check      # fmt + go fix + proto:lint + proto:check + vet + lint + build + test + test:ui (The Definitive Gate)
task dev        # archied live-reload + Vite HMR together on :5173
```

Single package/test runs:

```bash
go test ./internal/domain/workflow/... -run TestName -v -count=1
```

## Repository Hygiene

- **Generated assets are LAW:** Never modify, revert, or ignore generated files
  in `ui/dist/` or schema outputs. Incorporate them into relevant commits.
- **Ignored paths:** Never commit build artifacts, binaries (`/bin/`,
  `/archied`), DB files, working screenshots, `.references/`, or
  `node_modules/`.
- **Scratch files:** Place scratch files strictly in `/tmp` or the harness
  scratch space. Never put scratch files in the working tree
  (`worktree.CommitAll` will stage them).
- **Conventional Commits:** Scope by package: `feat(webui): ...`,
  `fix(build): ...`, `chore(release): ...`.
- **Commit policy:** Commit finished, gate-clean changes (`task check`
  passing) without asking. Push only when instructed.

## Organisation (Strict Domain-Driven Architecture)

`docs/architecture/organisation.md` is authoritative. Do not follow flat
structures found in legacy packages.

### Layers and Ownership

- `internal/domain/<area>/`: Domain logic, entities, state machines, commands,
  events, and required contracts (interfaces). Never imports infrastructure or
  app.
- `internal/infrastructure/<service>/`: Implementations of domain contracts
  (persistence, forge clients, external transports, config loading).
- `internal/app/<application>/`: Dependency injection, wiring domain to
  infrastructure, lifecycle management, and shutdown ordering.
- `cmd/<binary>/`: CLI flag parsing, OS signal traps, and entry point routing
  only. No domain wiring.

### Invariants

- **Dependency Flow:** `cmd → app → {domain, infrastructure}`, with
  `infrastructure → domain` implementing contracts. Downward/inward dependencies
  only.
- **Cross-Cutting Packages:** `logging`, `events`, `eventbus`, `policy`,
  `taskstate` sit directly under `internal/`. They may be imported by any layer,
  but **must import zero internal packages**. No `shared/`, `utils/`, or
  `common/` catch-alls.
- **File Layout:** One file per API concern (`api_tasks.go`, `api_logs.go`). A
  package owns its on-disk format end-to-end (e.g. `internal/logging` defines
  and reads its format; transport layers do not parse).
- **Frontend:** Features live in `ui/src/<feature>/` with colocated `.js` and
  `.css`. Extract to `ui/src/base/` only on the second distinct consumer.

## Per-Package Invariants & Traps

- **`internal/channels/telegram/`:**
- The long-polling worker must call `dropPendingUpdates(ctx, b)` before
  `b.Start(ctx)` and on `/restart`.
- Always publish command menus to `default`, `all_private_chats`, and
  `all_group_chats` simultaneously via `commands.go` to avoid scope shadowing
  from legacy registrations.

- **`ai-sdk` Streaming:** `FullStream` writes synchronously and MUST be drained
  completely to prevent producer deadlocks. Do not rely on `TextStream` (drops
  deltas).
- **NATS Invariants:**
- `ARCHIE_TASKS` uses `jetstream.WorkQueuePolicy`. Do not bind overlapping
  consumer filters; use `Fetch` on existing durables instead of new `Subscribe`
  calls.
- RPC and task responses must use core NATS `msg.Respond` to write to ephemeral
  `_INBOX.*` subjects, not `eventbus.Publisher`.
- Always flush connections (`nc.Flush()`) immediately after registering
  responders to avoid race conditions on initial requests.

- **`internal/gateway/` Chat Prompt:** System prompt `<tools>` blocks are
  rendered strictly from the active `core.ToolSet`. Never decouple prompt tool
  definitions from passed runtime tools.
- **`internal/container/pool.go` (`WriteTaskJSON`):** Write task briefs to
  `<worktree>/.git/task.json`. Never place them in the worktree root (`go-git`
  `Add{All:true}` ignores `.gitignore` and leaks root files into branch
  commits).
- **MCP Providers:** Daemon registers providers as optional via
  `providerRegistry.RegisterOptional`. Missing or failed providers log warnings
  and degrade health without terminating the process.
- **`internal/infrastructure/staterpc/` (State Store gRPC contract):**
  Authority is `docs/prds/state-store-contract.md` (rev. 2c) -- read it before
  changing this package or its callers.
- The proto (`proto/state/v1/state.proto`, service `StateStoreService`,
  package `statev1` in `internal/contracts/state/v1/`) is one gRPC service
  fronting every ratified store contract (42 RPCs); the Go consumer facades
  stay narrow (`workflow.Store`, `store.TaskStore`, etc., all ≤8 methods
  except the `TaskStore` composite) via `staterpc.Client`'s multiple `var _`
  assertions -- never add a Go interface method without a matching RPC.
- Error sentinels (`store.ErrStaleTransition`, `ErrBindingNotFound`, ...)
  cross the wire via `mapError`/`unmapError` in `values.go`, matched on
  `(code, exact canonical message)`. Changing a canonical message string
  breaks `errors.Is` on the client without changing behavior visibly --
  treat those message constants as part of the wire contract.
- The State Store is a standalone process (`cmd/archie-state-store`, run via
  `archied.RunStateStore`) -- the daemon and Gateway never own `archie.db` or
  serve this service in-process; `boot.openStateStore` in
  `internal/app/archied/state_store.go` is the only caller of
  `openProductionTaskStore`. Never add a second listener for the same
  service, and never reintroduce an in-process serving path in the
  daemon/Gateway (`TestOpenStoresNeverOwnsTaskDB` guards this).
- Per-task credentials are scoped, not just authenticated: `daemon.
  StateStoreGrantIssuer` (`staterpc.GrantIssuer`) registers a fresh
  task-scoped grant at the remote State Store via `RegisterTaskGrant` before
  `ContainerPool.Acquire` and revokes it via `RevokeTaskGrant` on `Release`
  (`acquireTaskContainer`/`process` in `internal/daemon/daemon.go`). Fails
  closed (parks the task) if a State Store is configured but no issuer is
  wired -- it must never fall back to forwarding the daemon's own
  administrative token. Server-side, `staterpc.TaskGrants.UnaryInterceptor`
  authorizes a task-scoped token for only `Update`/`Transition`/`InsertEvent`
  on its own task ID; every other RPC (including `RegisterTaskGrant`/
  `RevokeTaskGrant` themselves and the streaming capture RPCs) requires the
  administrative token.
- `archie-agent` has exactly one `workflow.Store` path: gRPC via
  `staterpc.Client` (`agenttransport/nats.Transport.Store`), using the
  `STATE_STORE_URL`/`STATE_STORE_TOKEN` env the daemon injects. The legacy
  NATS `storerpc` transport is deleted (`internal/storerpc` no longer
  exists); do not reintroduce a dual-path selection.
- `store.BindingDispatcher.RecordDispatch` takes no `*sql.Tx` (dropped in
  `.4.2` -- it cannot cross a gRPC boundary; production always passed `nil`).
  Do not reintroduce a transaction parameter on a producer-owned store
  interface that a remote adapter must also implement.
- **`internal/forge/webhook/` (forge webhook receiver):**
- Verify HMAC (`webhookguard.VerifyHMAC` against `X-Hub-Signature-256`) before
  parsing the payload, never after -- an unverified body must not reach
  `github.ParseWebHook`.
- Do not give the webhook-built `workintake.TaskEnvelope` a delivery-source-
  specific idempotency key. `IdempotencyKey()` is keyed on `owner/repo/number`
  only so a webhook-delivered issue and a later poll of the same issue dedup
  against each other via `PublishUnique`; adding an event ID or timestamp to
  the key defeats that and reintroduces poll/webhook double-dispatch.
- Webhook intake refuses to start when `[[identities]]` is configured (one
  receiver, one dispatch config). Do not silently enable it for multi-identity
  deployments without first building per-identity routing -- see
  `ARCHITECTURE.md`'s "Webhook intake" section.
- **`internal/domain/embedding/` + `internal/infrastructure/embedding/`:**
  Embedding client contract + implementation. Config-driven exactly like chat
  model roles: `models.embedding = "provider/model"` plus a `[providers.*]`
  entry. `infrastructure/embedding.New` degrades to `(nil, false)` -- never an
  error -- for a missing role, unknown provider, unsupported class, or
  unresolved credential; wired as an optional capability in
  `bootstrap.go`'s `setupEmbeddings`. Only provider classes matching
  `config.Provider`'s shape are supported (openai, gemini, ollama, cohere,
  mistral) -- azure needs a `Deployment` field this config type doesn't have.
- **`internal/domain/sampling/`:** The curator `Sampler` interface (see
  `docs/prds/curator-sampler-wave1.md`) plus four pure, deterministic
  strategies (recency, staleness, random, all). No curator consumes a
  `Sampler` yet -- `curator.Registrar` is untouched by this package.

## Development Protocol

1. **Red (Failing Test):** Write failing test cases first using table-driven
   tests against target behaviour. Verify the failure originates from test
   assertions, not compilation errors.
2. **Green (Implementation):** Implement minimal code to satisfy the failing
   tests.
3. **Quality Gate:** Run `task check`.
4. **Formatting is LAW:** Adopt all formatting and simplification changes from
   `task fmt` (`gofumpt` + `go fix`) verbatim. Never revert or fight canonical
   linter/formatter diffs.
5. **No standing adversarial-review pass (for contributors):** Do not spawn a
   separate fresh-context reviewer pass on every change as a matter of course.
   Red-green TDD plus `task check` is the gate; a manual adversarial pass is
   opt-in per request — ask before running one. This is the rule for a human or
   agent *contributor*. Separately, archied runs the same adversarial review
   automatically on the PRs it opens (gated per repo by `repo.review_enabled`)
   — see `docs/architecture/adversarial-review.md`. The two paths are distinct:
   you run one when asked; archied runs one before it opens a PR.
6. **Linter Guard:**

- When using `errorlint` fixes, ensure boolean predicates (e.g.
  `exitErr.ExitCode() == 1`) are retained alongside `errors.As`.
- When extracting loops/conditionals, verify slice mutations and pointer
  semantics do not duplicate elements.

## Issue Tracking (Beads)

All task management is tracked via **bd (beads)**. Do not write local Markdown
TODO lists or use non-Beads trackers.

### Commands

```bash
bd ready               # List unclaimed work
bd show <id>           # View issue details
bd update <id> --claim # Claim an issue
bd close <id>          # Mark issue complete
bd remember            # Persist cross-session architectural facts
```

- Run `bd prime` to inspect full engine context.
- Use `bd remember` for knowledge retention. Do not write to root `MEMORY.md`
  files.
- Issues live in a local Dolt DB, synced to its own `git+` remote via
  `bd dolt push`/`bd dolt pull`. `.beads/issues.jsonl` and
  `.beads/interactions.jsonl` are local-only export/audit files
  (`.gitignore`d, `export.auto: false`) -- never `git add` them and never
  create a commit whose only content is beads bookkeeping. Bead state moves
  through Dolt, not through git commits on this repo.

## Session Completion Protocol

1. **Log remaining work:** File new items via `bd` for identified debt or
   follow-up tasks.
2. **Run gate:** Verify `task check` passes completely clean.
3. **Update tracker:** Close finished issues via `bd close <id>`. This alone
   is not commit-worthy -- do not stage or commit `.beads/` for it.
4. **Commit:** Only for actual code/doc changes, scoped to the files that
   changed.

   ```bash
   git add <scoped-files>
   git commit -m "feat(scope): description"
   ```

5. **Sync & Push Policy:** Do not push to git remotes or run `bd dolt push`
   unless explicitly instructed.
6. **Handoff:** Report changed files, gate verification results, and active
   issue states.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
