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
task check      # fmt + go fix + vet + build + test + test:ui (The Definitive Gate)
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
- **Commit policy:** Commit finished, gate-clean changes (`task check` passing +
  clean adversarial review) without asking. Push only when instructed.

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
5. **Adversarial Verification:** Run a verification pass using an isolated
   reviewer context assuming all changes are incorrect until verified. Check:
   dead code, unchecked errors, hardcoded constants, missing error paths, nil
   pointers, goroutine leaks, and race conditions.
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
- Issues live in a local Dolt DB; `.beads/issues.jsonl` is an export.

## Session Completion Protocol

1. **Log remaining work:** File new items via `bd` for identified debt or
   follow-up tasks.
2. **Run gate:** Verify `task check` passes completely clean.
3. **Update tracker:** Close finished issues via `bd close <id>`.
4. **Commit:**

   ```bash
   git add <scoped-files>
   git commit -m "feat(scope): description"
   ```

5. **Sync & Push Policy:** Do not push to git remotes or run `bd dolt push`
   unless explicitly instructed.
6. **Handoff:** Report changed files, gate verification results, and active
   issue states.
