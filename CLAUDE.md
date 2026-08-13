# CLAUDE.md

Guidance for any coding agent working in this repository.

`AGENTS.md` is a symlink to this file, so Claude, Codex, Pi, Tau and anything
else reading either name get the same instructions. **Keep every rule here
harness-agnostic**: name the behaviour required, not the tool that provides it.
"Spawn a fresh reviewer that did not write the code" is a rule; "run
`/code-review`" is one harness's way of satisfying it. The single exception is
the memory note under Issue Tracking, which is explicitly Claude-scoped.

## What this is

**archied** is a resident daemon that polls a forge (GitHub, Gitea planned) for
issues assigned/labelled to it, works each one in an isolated git worktree
through a routed workflow (bootstrap / implement / TDD / feasibility), and opens
pull requests for human review. See `ARCHITECTURE.md` for the full package map,
workflow engine design, task lifecycle, and key design decisions — read it
before making non-trivial changes, since several invariants (env-enforced gates,
model never runs git, agent execution as a data boundary) aren't obvious from
the code alone.

## Scope discipline (read this before planning anything)

The architecture documentation in `docs/architecture/` is **complete and
authoritative**. It is a design to implement against — not a draft to extend.

- Do **not** produce new architecture documents, migration plans, ownership
  ledgers, field-level inventories, current-state traces, decision records, or
  review campaigns. If a design question isn't answered in `docs/architecture/`,
  ask the maintainer in one sentence — don't write a document to resolve it.
- Do **not** open planning, review, or refactor issues. Open issues only for
  concrete bugs and user-facing features.
- This is a **solo project**. There is no review board, no handoff to a
  zero-context engineer, no compliance requirement. Prefer the smallest change
  that works over the most defensible one.
- Default to producing a diff. If a task's output would be a document rather
  than code, confirm that's actually wanted before starting.
- Issues are tracked in GitHub Issues, grouped issues can be linked under an epic, 
  with subtasks linked to the epic.

## Deployment model

**archied supports several deployment shapes; systemd is not required.**
`deployments/` holds the supported profiles — `single-forge-github.toml`,
`multi-forge-github-gitea.toml`, `local-ollama-standalone.toml`,
`docker-nats-stack.toml`, and `systemd-user-service.md` as one operational
runbook among them. Do not write code or docs that assume a systemd unit exists.

**The chat agent's file tools are workspace-confined by default.** `builtin`
tool construction calls `SetPathConfinement(!unrestricted)`, so read, write,
edit, find and grep are jailed to the configured workspace unless
`[chat] unrestricted_filesystem = true` lifts it
(`internal/config/config.go`, `internal/tools/provider/builtin/provider.go`).
That setting is commented out in `config.example.toml` — an operator opt-in, not
the default, and not an architectural requirement. Where the daemon runs and
whether the jail is lifted together determine which filesystem those tools can
reach; neither is something the code should assume. NATS and the per-task
archie-agent containers are Docker regardless.

**The daemon hands agent containers the NATS endpoint its own client connected with at startup** — see
`containerEnv` in `internal/daemon/daemon.go`, which appends `NATS_URL=` from
`d.ConnectedNATS.URL`, captured at construction. That endpoint must therefore be reachable from wherever the daemon
runs *and* from inside the containers. When the daemon is on the host, that
means the compose network's gateway address, not the service name and not
localhost. `docker-compose.yml` pins the subnet to `172.19.0.0/16` so the
gateway address cannot drift when the network is recreated.

The two URLs are the same today and deliberately *not* the same after a
SIGHUP reload of `nats.url`: the daemon's own client is startup-built, so a
reload that pointed new containers at the new URL would launch them on a
server the daemon is not publishing on, and every task would park with
`ErrNoResponders`. Freezing the connected endpoint makes that divergence
impossible regardless of what the config says or who reloads it; a reloaded
`nats.url` is logged as a change that requires a restart.

**`containers.pull_policy` can only be `"missing"`** (the default) or
`"always"`. `"always"` against a private registry answers 401, because the
daemon sends no credentials to the Docker API. Refresh the agent image
explicitly instead: `docker compose pull agent`.

**`docker-compose.yml`** carries only NATS and the agent build stanza. The agent
has a `build` profile so `up -d` skips it — without the profile, compose creates
a container that exits 0 immediately and reports "Started", which reads as a
crash loop. `docker compose build agent` and `docker compose pull agent` still
work by name without activating the profile.

**Config lives outside the repo**, at
`${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`. Nothing in the repo working
tree is load-bearing at runtime. Reference templates are in `deployments/`;
`config.example.toml` is the annotated schema.

## Release process

Two components, independently versioned: `archied` (gateway/daemon) and
`archie-agent` (runtime). Tags use `archied/vX.Y.Z` and `archie/vX.Y.Z`.
`CHANGELOG.md` documents the split.

```
task release:preview VERSION=1.3.0     # preview what would land
task release:prepare VERSION=1.3.0     # write changelog sections, uncommitted
# edit CHANGELOG.archied.md and CHANGELOG.archie.md  --  generated notes
# are a starting point, not the release
task release VERSION=1.3.0             # commit + tag
git push origin main --follow-tags     # CI stamps images with real versions
```

Pass `GATEWAY=<ver>`/`RUNTIME=<ver>` to version them independently, or `skip` to
hold one back. Linting ensures the changelogs can be parsed deterministically by
`tools/release.sh`.

CI (`deploy.yml`) now runs a gate job _before_ building images: `task check`,
then verifies that any release tag at HEAD has a matching `[version]` section in
the corresponding changelog. A tag without a changelog entry is a hard failure.
No tags at HEAD prints a warning (images get stamped `dev`).

## Build & Test

Commands are defined in `Taskfile.yml` (uses [Task](https://taskfile.dev)); run
`task --list` to see everything.

**Prerequisites:** Go 1.26.5 (matched exactly by `go.mod`), [Task], `gofumpt`,
`golangci-lint`, and **Node/npm** — `task check` runs the dashboard suite too, so
a Go-only toolchain cannot complete the gate.

[Task]: https://taskfile.dev

```bash
task build      # build archied and archie-agent binaries into bin/
task test       # go test ./... -count=1
task test:ui    # dashboard node tests (DOM-building primitives)
task ui         # build the dashboard into ui/dist (committed — see below)
task fmt        # gofumpt -w . && go fix ./...
task vet        # go vet ./...
task lint       # golangci-lint run ./...
task check      # fmt + go fix + vet + build + test + test:ui
task dev        # archied live-reload + Vite HMR together, on :5173
```

`task check` is the gate, and it includes `test:ui` deliberately: nothing else
runs the dashboard suite, so left out it rots silently and a broken DOM
primitive ships. Note that `task fmt` **rewrites files**, so `check` is not a
read-only verification step.

Run a single test: `go test ./internal/domain/workflow/... -run TestName -v`.

Docker/NATS stack: `task docker-build` (builds the agent image locally),
`task docker-up`, `task docker-down`, `task docker-logs`.

## Repository hygiene

- **`ui/dist` is committed on purpose.** The Go build embeds it, so building
  `archied` must never require a Node toolchain. If you change anything under
  `ui/src`, run `task ui` and commit the rebuilt `ui/dist` in the same change.
  Never add it to `.gitignore`.
- **Do not commit** build artefacts or local state: `/archied`, `/archie-agent`,
  `/bin/`, `*.db`, `*.png` working screenshots, `.references/`,
  `config.production.toml`, `.env`, or the docs site's `node_modules/` and
  `.vitepress/{cache,dist}/`. `.gitignore` already covers these; the point is not
  to defeat it.
- **Scratch files go outside the repo**, in the OS temp directory or your
  harness's scratch area — not in the working tree, where `worktree.CommitAll`
  will sweep them up (see the `WriteTaskJSON` trap below).
- Commits follow **Conventional Commits** scoped by package:
  `feat(webui): ...`, `fix(build): ...`, `ci: ...`, `chore(release): ...`.
- Only commit or push when asked.

## Organisation (strict)

`docs/architecture/organisation.md` is authoritative and approved. This section
is the enforcement summary — read that doc before any structural change, and
change the doc first if the rule itself needs to change.

**Archie is domain-driven, not flat.** `internal/` currently holds ~35 packages
side by side. That is a migration in progress, not the target. Do **not** infer
the structure from what is there: adding a sibling package next to
`internal/gateway/` because that is where similar code lives today is how the
flat layout keeps regrowing, and undoing it is measured in weeks.

### The layers, and what each owns

- **`internal/domain/<area>/`** — one cohesive job Archie performs. Owns that
  job's language, state, rules, operations, commands, events, policy
  implementations, runtime settings, and the _contracts_ it needs from the
  outside world. A domain declares the interface it needs; it never names who
  implements it.
- **`internal/infrastructure/<service>/`** — implementations of those contracts:
  configuration, persistence, forge clients, event-bus transports, anything that
  talks to something external.
- **`internal/app/<application>/`** — composition. Constructs domains and
  infrastructure, translates external configuration into domain settings,
  connects commands and events, starts services in dependency order, owns health
  aggregation and shutdown ordering. It is the only layer that knows about both
  of the two above.
- **`cmd/<binary>/`** — process-level input only: flags, environment, signals.
  It MUST NOT contain substantive wiring and MUST NOT act as a service locator.

These stay under `internal/`. That is a compiler-enforced visibility boundary,
not a naming convention: moving a layer to the repository root would make
Archie's domain types importable by any module that depends on this one, and
silently commit us to API stability on exactly the code that most needs to keep
changing.

### Dependency direction is the invariant

`cmd → app → {domain, infrastructure}`, and `infrastructure → domain` to
implement its contracts. Nothing points back up: a domain importing
infrastructure, or infrastructure importing app, is a defect regardless of how
convenient it is. If a domain needs something an infrastructure package has, the
domain declares an interface and app wires the implementation in.

### Cross-cutting packages

Some packages are neither domain nor infrastructure because they serve both —
`logging`, `events`, `eventbus`, `policy`, `taskstate`. They sit as named
top-level packages under `internal/`, each named for what it provides.

The defining property is checkable, and it is the whole rule: **a cross-cutting
package may be imported by any layer, and imports none of them.** Zero
dependencies on domain, infrastructure or app. That is what lets it sit outside
the dependency direction safely — it is a sink, never a source, so it cannot
create a cycle or smuggle a dependency inward.

It is also the failure test: the moment such a package imports a domain type it
has stopped being cross-cutting and become domain code that has been misfiled.

There is deliberately **no `shared/` directory**. "Shared" is a category, not a
name, and a directory named for a category becomes the place things go when
nobody can say what they are. `utils`, `helpers`, `common` and `misc` are
prohibited for the same reason: if a package cannot be named for what it
provides, it is misplaced, not shared.

Cross-cutting utilities exist precisely to keep the code DRY — a function owned
by no single package and used by many. Do not wait for a second consumer to
justify one, and do not copy a helper into two domains to avoid creating one.
(The "second consumer" rule below is about feature code, not this.)

### Within a package: organise by concern, never by layer or type

- One file per API area, named for it: `api_tasks.go`, `api_logs.go`,
  `api_skills.go`. A new endpoint group is a new file. Cross-cutting wiring gets
  its own file too: `server.go` (routes and construction only), `auth.go`,
  `sse.go`.
- **A package owns its own format end to end.** `internal/logging` defines the
  on-disk log format _and_ reads it (`reader.go`); `internal/webui/api_logs.go`
  is transport and parses nothing. If two packages both know a format, one of
  them is wrong.
- Empty or ceremonial `entities`, `repositories` or `services` layers are
  prohibited. Code that changes together lives together.
- No grab-bag file. If you cannot name a file after the single thing it does, it
  is doing more than one thing.

### Frontend

- One folder per feature under `ui/src/<feature>/`, holding its own `.js` and
  `.css`, with the JS importing its own CSS. Changing logs cannot disturb tasks.
- Shared primitives live in `ui/src/base/` and `ui/src/css/`, one file per
  primitive (`button.css`, `card.css`, `pill.css`, `table.css`).
- Extract a feature's code into a shared primitive on the **second** real
  consumer, never in anticipation of one.

### When to split

Split on concern, not line count. The triggers are: you scroll past unrelated
code to change one thing, or two people editing different features collide in
one file. Renaming a file to something vaguer to make new code fit is the
failure this rule exists to prevent — add a file.

### Why

A domain that declares its own contracts can be tested, reviewed, replaced and
reasoned about without the rest of the system. Once the direction of dependency
inverts anywhere, that stops being true everywhere downstream. It also has an
immediate cost with parallel sessions: `cmd/archied/main.go` had to be split
hunk-by-hunk three times in one day because two sessions were editing it at once
— a symptom of wiring living where it should not.

## Architecture

Full details live in `ARCHITECTURE.md` — do not duplicate it here; skim it
whenever touching the daemon, workflow engine, or forge/worktree/store
boundaries. Summary of what's evolved since that doc was last fully accurate:

- **The domain migration has begun.** `internal/domain/` (curator, workflow,
  workintake) and `internal/infrastructure/` (configuration, eventbus,
  modelcatalog, terminalprompt) both exist. The tree is authoritative over any
  package table; see `docs/architecture/migration-decisions.md` for the recorded
  position and what was agreed next.
- The daemon has grown an RPC split (`internal/forgerpc`, `internal/storerpc`,
  `internal/worktreerpc`, `internal/natsrpc`) alongside the in-process model
  described in ARCHITECTURE.md. The eventbus transport is
  `internal/infrastructure/eventbus/`. Check those plus `docker-compose.yml` for
  the current split between `archied` and sandboxed workers.
- `internal/gate/` — quality gate execution (the "environmental constraints over
  prompt rules" mechanism).
- `internal/skill/`, `internal/skillscript/`, `internal/yaegiutil/` — SKILL.md
  support (listed as "Planned" in ARCHITECTURE.md; now has real implementation —
  check these packages for current state before assuming it's unbuilt).
- `internal/channels/` — inbound chat/notification channels (e.g. Telegram — see
  `internal/channels/telegram/`, `internal/channels/email/`); channel-agnostic
  interface in `internal/channels/channel.go`.
- `internal/memory/`, `internal/gateway/`,
  `internal/container/`, `internal/plugin/`, `internal/tools/`,
  `internal/taskrun/` — not documented in ARCHITECTURE.md; read the package
  before modifying, its doc comment and tests are the source of truth.

### Per-package traps (things an agent will break if it doesn't check first)

- **`internal/channels/telegram/`** — the long-polling branch calls
  `dropPendingUpdates(ctx, b)` before `go b.Start(ctx)`. This clears Telegram's
  24h undelivered-update queue so a reboot doesn't answer hours-old messages. It
  also runs on `/restart`. The webhook branch already passes
  `DropPendingUpdates: true` in `SetWebhookParams`; both paths must stay
  consistent.
- **Telegram command menus resolve by SCOPE SPECIFICITY, not recency.** Order,
  most specific first: `chat_member` > `chat` > `chat_administrators` >
  `all_chat_administrators` > `all_group_chats` > `all_private_chats` >
  `default`. So `SetMyCommands` on `default` alone is **invisible in DMs** if
  anything was ever registered on `all_private_chats`. Hit when migrating a bot
  token between applications: our 8 commands landed on `default` and logged
  success while the menu still showed ~60 stale commands from the previous app.
  Menus are server-side state owned by Telegram and keyed to the **bot token** —
  they survive redeploys, rewrites and whole-application migrations. Inheriting a
  token means inheriting its menu, webhook config and other bot-level settings,
  so audit `getMyCommands` per scope and `getWebhookInfo` when taking one over.
  `commands.go` publishes to `default` + `all_private_chats` + `all_group_chats`
  so leftovers are overwritten rather than shadowing.
- **`ai-sdk` streaming: `FullStream` is authoritative and MUST be drained until
  close.** Its writes are *synchronous*, so an unread `FullStream` stalls the
  producer and deadlocks the turn. `TextStream` is best-effort and silently
  DROPS deltas when not drained — consuming it deadlocked every streamed reply
  in production. Range `FullStream` and collect `core.StreamPartTextDelta`. See
  the doc comment on `core/stream_impl.go`. Related: check the pinned
  `go-telegram/bot` version against latest before hand-rolling anything — a
  Markdown→HTML converter was written while 4 releases behind, and
  `SendRichMessage` with `models.InputRichMessage{Markdown}` had made it
  obsolete. `EscapeMarkdown` does the *inverse* of formatting, and
  `ParseModeMarkdown == "MarkdownV2"` while `ParseModeMarkdownV1 == "Markdown"`
  — the names disagree with the values.
- **NATS, three behaviours that have each caused a real defect:**
  (1) *Work-queue retention forbids overlapping consumers.* `ARCHIE_TASKS` uses
  `jetstream.WorkQueuePolicy`, so two consumers with overlapping `FilterSubjects`
  conflict and the later one silently receives nothing. `nats.Client.Subscribe`
  creates a NEW consumer, so calling it on subjects the client's own durable
  consumer already filters will never deliver — drain the existing consumer with
  `Fetch` instead, as `internal/app/agentworker` and the agentexec round-trip fake do.
  (2) *Reply inboxes are not stream subjects.* Answering a request must use core
  NATS publish, not the JetStream path: a `Message.ReplyAddress` is an ephemeral
  `_INBOX.*` belonging to one waiting caller and part of no stream, so a durable
  publish to it fails. That is why `eventbus.Publisher` declares `Respond` as a
  method distinct from `Publish` rather than a convenience over it.
  (3) *Subscribe does not mean reachable.* `nc.Subscribe` only queues the SUB
  frame client-side, so code that registers a responder and immediately issues a
  request can beat its own subscription and get `ErrNoResponders` from a
  responder that is, by then, listening. `natsrpc.RegisterAll` now flushes before
  returning — this was the cause of the long-standing
  `TestRunTaskExecutesBootstrapWorkflowEndToEnd` CI flake that passed in
  isolation and failed under full-suite load.
- **`internal/gateway/` chat prompt** — `BuildSystemPrompt` renders
  `templates/archie.md.tpl`. The `<tools>` block is built from the **same**
  `core.ToolSet` passed to the model, so the prompt cannot claim a tool the model
  cannot call; never source the tool list from anywhere else. An empty toolset
  deliberately renders "no tools are available" rather than an empty block,
  because an empty block reads as "you have tools" and the model invents them.
  Personas are the STYLE layer only — instruction precedence, core rules and the
  `<tools>`/`<env>` blocks live in the template, so selecting a persona can never
  strip rules or tool inventory. Do not move rules into persona strings.
  `internal/tools/provider/contract_test.go` asserts the literal wiring strings
  `chatGenerateOptions(nil, toolReg` and `toolSummaries(options.Tools)` in
  `cmd/archied/main.go` — refactoring that call site fails that test **by
  design**.
- **`internal/container/pool.go` `WriteTaskJSON`** — the per-task boot brief
  goes to `<worktree>/.git/task.json`, not the worktree root.
  `worktree.CommitAll` stages via go-git `Add{All:true}`, which ignores
  `.gitignore` and `.git/info/exclude`, so anything in the worktree root is
  pushed onto the task branch. Nothing under `.git` can be tracked;
  `worktree.go:41-46` documents this reasoning for the prepared sentinel. If you
  change the path, update `internal/infrastructure/agentboot/task.go` `TaskID`
  (the reader) in lockstep.
- **MCP providers are registered by the daemon as _optional_**
  (`cmd/archied/ main.go`, `providerRegistry.RegisterOptional`). One that cannot
  start is logged at error level, excluded from the running set, and reported
  through `Registry.Health` as degraded — it does not roll back the other
  providers or stop the daemon. That was previously a hard failure, so one
  broken npm package unregistered every builtin tool and crash-looped archied
  under `Restart=on-failure`, taking Telegram with it. Providers the daemon
  genuinely requires still use `Register` and keep atomic rollback; check
  `providerRegistry.Skipped()` when tools are mysteriously missing. No
  npx-launched servers are configured in production anyway — the builtin tools
  (read, write, edit, find, grep, shell) and `web_fetch`. There is no `test`
  tool; gates run test commands, the model does not.

**Task lifecycle** (from ARCHITECTURE.md, still current):
`queued → running(workflow:stage) → pr_open → merged|rejected`, with
`waiting_human` and `parked` side states. Crash recovery re-queues anything left
`running`. A park records its reason on the task, which the dashboard and
`/api/tasks` surface; it no longer comments on the forge issue. Retries are an
explicit operator action (dashboard or chat), capped by `max_retries`, not a
label a human removes.

**Config** is daemon-level TOML (see `config.example.toml`), not per-repo files
in this repo. Multi-identity deployments run more than one identity from a
single daemon, each with a distinct `bot_user`; see
`deployments/multi-forge-github-gitea.toml` and `docs/architecture/identity.md`
before changing multi-identity or dispatch behaviour. Details of any particular
operator's hosts belong in that operator's own notes, not here.

## Conventions

- Go 1.26.5, module `github.com/samcharles93/archie-core`.
- `ai-sdk/runtime`, `ai-sdk/agentloop`, `ai-sdk/core` are external other
  projects, not vendored copies. Changes affect other repos.
- **Commit the meaning, not just the change.** When a change alters what
  something MEANS rather than what it does, say so explicitly in the commit body
  and in the code comment: "X used to mean A; it now means B, because C." Update
  the doc comment in the same commit — a comment that contradicts its code is
  worse than no comment, because the next reader trusts it. This is not
  ceremony: over 2026-08-04/05, three consecutive rounds of fixes each introduced
  a regression the next round caught, and **every one passed `task check` with
  tests written for it** (a summary that sorted to the end of history; a cache
  slot claimed before the store write; a degraded container pool that silently
  ran agent loops in-process with the daemon's own authority, removing the
  isolation boundary `[containers]` exists to provide). Tests catch behaviour
  that breaks; they do not catch behaviour that quietly starts meaning something
  else. Roughly half of what adversarial reviewers found, they found by noticing
  a doc comment or commit message that no longer matched the code.
- **Lifting code from tau requires a provenance header** on every copied file,
  naming the tau source path, the tau commit it came from, and the deliberate
  mutations. This turns copy-paste drift into a diff instead of archaeology.
  (Reuse strategy is copy-and-mutate, never extraction into shared libraries.)
- Non-interactive shell commands only: some environments alias `cp`/`mv`/`rm` to
  `-i`. Use `cp -f`, `mv -f`, `rm -f`/`rm -rf`, `scp -o BatchMode=yes`,
  `ssh -o BatchMode=yes`, `apt-get -y`.
- Environmental enforcement over prompt rules: gates, test-file protection, and
  diff caps are code-level checks the agent cannot talk its way around — follow
  this pattern for any new safety constraint rather than adding it to a system
  prompt.
- **Plugin engine rule (strict):** “engine” means a typed, lifecycle-managed
  capability family with an owning registry or manager; it never means another
  method on the generic plugin interface. Keep `plugin.Plugin` metadata-only,
  give plugins narrow typed registrars instead of daemon access, and apply the
  lifecycle and trust-boundary requirements in
  [ARCHITECTURE.md#plugin-engine-rule-strict](ARCHITECTURE.md#plugin-engine-rule-strict).
  Its mechanically testable structure is enforced by
  `internal/plugin/architecture_test.go`; reviewers enforce its lifecycle,
  access, and trust semantics.
- The model/agent never runs git directly — worktree operations
  (`internal/worktree/`) are deterministic daemon-owned steps, not agent tools.

## Development Protocol (mandatory)

This is not optional style guidance — every code change in this repo follows
this sequence. It's the same discipline archied enforces on _itself_ via the
TDD workflow in `ARCHITECTURE.md`.

**`task check` is the definitive quality gate.** `task check` = `fmt` +
`go fix ./...` + `vet` + `build` + `test`, and it is the one command that must
pass, clean, before any change is considered done — it's the literal analogue of
the `[[repos.gate]]` archied runs against every PR it opens. Don't hand-pick a
subset of `go test`/`go vet`/`go build` and call it good; run `task check` (or
the scoped equivalent, e.g. `go test ./internal/foo/... -count=1` +
`golangci-lint run ./internal/foo/...` for a single package) and confirm it's
clean.

**Red-then-green, definitively:**

1. **Red** — write the failing test(s) first, against the _intended_ behavior,
   before any implementation code exists. Run them and confirm they fail for the
   right reason (not a compile error).
2. **Green** — write the minimum implementation to make those tests pass. No
   implementation code is written before its test exists and fails.
3. Confirm via `task check` (or scoped `go test .../... -count=1`) that the
   suite is green, `golangci-lint run` reports zero findings, and `gofumpt -w .`
   reports no unformatted files.
4. No hardcoded values in tests (use parameterized test tables), no hardcoded
   identity/config values in implementation code, and every error path gets a
   test.

**Adversarial verification is mandatory, and it runs as a separate pass from a
fresh reviewer — never self-certified by the implementer.** After red-then-green
is green:

- Run the review from a context with no memory of how the code was written, and
  instruct it to assume every line is wrong until proven correct. Any mechanism
  that achieves this is fine — a fresh subagent, a separate session, a review
  command your harness provides.
- The checklist: all tests pass, lint is zero-findings, formatting is clean,
  then a manual read of every non-test file in the changed package for — dead
  code paths, unchecked error returns, hardcoded values that should be
  parameters/constants, interface satisfaction, nil-pointer risk, goroutine
  leaks, and races on shared mutable state.
- Findings are **reported**, not silently fixed by the same pass that wrote the
  code — a finding that survives blocks closing the work, it doesn't get waved
  through. (If your harness has a structured findings mechanism, use it;
  otherwise a plain list is fine.)
- Only close out the issue once the adversarial pass reports zero surviving
  findings.

**Lint-driven changes are behaviour-bearing. Review them; never trust them.**
Two confirmed regressions, one from each shape lint work takes:

1. **Autofix.** A `golangci-lint --fix` batch across 8 files had its `errorlint`
   fix in `internal/tools/builtin/grep.go` rewrite
   `if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1` into
   `errors.As(err, &exitErr)` **without the exit-code check**. ripgrep exit 1
   means no matches; exit 2 means the search never ran. So every failure was
   reported to the model as "no matches found", and the agent would state a file
   contains nothing on the strength of a search that never happened. The fix kept
   `errors.As` **and** restored the predicate — errorlint reports 0 issues on
   that form, so satisfying the linter never required dropping it. *Pattern:*
   fixers rewrite the error-matching part of a compound condition and can widen
   or narrow the predicate around it. Any autofix touching a condition needs its
   pre-fix behaviour checked.
2. **Structural extraction** — the riskier shape. Clearing a `nestif` finding
   extracted a helper that took a slice **by pointer** and appended every
   surviving field to it; the caller's own loop then appended the same fields
   again. Every model name doubled (`/model gpt-5.6` asked the router for
   `gpt-5.6 gpt-5.6`), and an index loop becoming a range loop meant a
   space-separated `--provider openai` value was no longer consumed with its
   flag. Telegram model switching broke outright and nobody noticed. *Pattern:*
   when a `gocognit`/`nestif`/`cyclop` extraction hands a collection to a helper
   by pointer, or turns an index loop into a range loop, check the accumulation
   and skip semantics. The compiler catches neither.

**Verification discipline:** confirm a regression test actually fails against the
buggy version before trusting it. The grep bug returned
`Content: "no matches found", IsError: false`, which a naive test asserting only
"no error" would have passed. For the doubled model name, assert **field count**
rather than string equality, so a future re-extraction cannot pass by doubling a
different part of the string.

## Issue Tracking

This project uses **GitHub Issues** (`samcharles93/archie-core`) via `gh` for
issue tracking. Epics are parent issues; their work items are linked as native
GitHub sub-issues. Labels: `task`, `feature`/`enhancement`, `bug`, `epic`,
plus `in-progress` for active work.

*Claude-specific:* project memory lives in
`~/.claude/projects/-work-apps-archie-core/memory/`. Other harnesses should use
their own equivalent — nothing in this repo depends on it.
