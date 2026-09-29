---
name: archie-agent-fleet
description: >-
  Run a bounded fleet of coding agents across archie-core's backlog without
  flooding review. Load when fanning work out into parallel lanes, waves,
  worktrees or subagents on this repository; when a lane worktree dies with the
  unknown-agent error for `cp-writer`; or when deciding what every lane call must
  carry, which gate evidence to demand, and how a lane's scoped test command
  should be shaped here. Carries archie-specific fleet facts only; the method
  lives in the user-scope high-throughput-programming skill.
---

# Run an agent fleet on archie-core

This skill carries the facts about _this repository_. It deliberately does not
restate the process.

## Where the method lives

The how is the user-scope skill `high-throughput-programming` at
`~/.agents/skills/high-throughput-programming/`: doctrine, pipeline shape, the
interview that scaffolds a run, the pi-subagents ceilings and traps, `scripts/`,
`templates/` and `references/`. Read its `INTERVIEW.md` before scaffolding a run;
never scaffold one from memory.

**Precedence.** That skill is normative for method; this one is normative for
archie's facts. If they disagree about the process, it wins. If they disagree
about this repository — a path, an invariant, a gate — this one wins, because the
method skill is repository-agnostic by construction.

Route runtime facts to `archie-run-and-operate`, toolchain facts to
`archie-build-and-env`, and dependency/boundary rules to
`archie-architecture-contract`.

## Invariants every lane call must carry

State these as facts to each lane, not as advice. Each one has cost a real run.

| Invariant                                                                                                                                                                                                                                                                                                               | Why it matters to a lane                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **The control plane fails closed.** A stored resource that will not validate stops `archied` starting, and nothing bypasses the control plane at boot. Resource history is the only in-band remedy, and it needs the State Store up.                                                                                    | A lane touching the boot path, a stored resource kind, or the recovery commands is a risk-routed lane for the operator's eyes, not a gate-and-merge lane. Evidence: `docs/architecture/safe-change-and-recovery.md` ("Database settings: degrade paths and recovery").                                                                                                                                                                                                                                                                                                                                             |
| **`internal/webui` must not reach `internal/domain/workflow`.** Go imports are atomic: one reference relinks the whole daemon runtime into the UI binary.                                                                                                                                                               | `cmd/archie-ui/architecture_test.go` measures the linked binary (`go list -deps .`), not the imports, and fails on anything under `internal/domain/workflow`. `internal/domain/workflow/task` is a named `gateExceptions` entry that must stay linked — it carries only contract and view types, and a stale exception fails the test in both directions. The same test's gate also excludes `internal/app/archied` from the UI binary, so a helper needed by both the daemon and the `cmd/` binaries belongs in `internal/infrastructure/configuration` (which already owns path resolution), never in `archied`. |
| **The task DB has one owner.** The task tables are served by the standalone `cmd/archie-state-store` process alone. No second listener, no in-process serving path in the daemon or Gateway.                                                                                                                            | `boot.openStateStore` (`internal/app/archied/state_store.go`) is the only place the task store is built, and it holds the `state-store` Postgres ownership lock for the process's life — a second State Store on the same database fails there rather than merely being discouraged. Offline maintenance belongs in subcommands of that binary, never a new `cmd/` entry (`docs/architecture/organisation.md#process-boundaries`).                                                                                                                                                                                 |
| **Never commit `.beads/`.** Bead state moves through Dolt; `issues.jsonl` and `interactions.jsonl` are local-only.                                                                                                                                                                                                      | A lane that stages `.beads/` pollutes its diff. Closing a bead is not commit-worthy on its own.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| **Generated assets are LAW.** `ui/dist/` and the schema outputs (`docs/data/generated/contracts.json`).                                                                                                                                                                                                                 | Incorporate them into the commit that shifted them. Never revert, regenerate away, or leave them out.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| **Scratch goes to `/tmp` only.** Never the working tree.                                                                                                                                                                                                                                                                | `worktree.CommitAll` stages with go-git's `AddWithOptions{All: true}` (`internal/worktree/worktree.go:502`), which does **not** honour `.gitignore`. An untracked scratch file in a lane worktree is swept onto the task branch. The task brief lives in `.git/task.json` for exactly this reason.                                                                                                                                                                                                                                                                                                                 |
| **A config field must survive the control-plane round trip or it does nothing.** A field added to `MCPServer` (or any projected settings type) must be carried in three sites: `mcpServerSettings` and `seedTools` in `internal/app/controlplane/tool_settings.go`, and `runtimeToolConfigFrom` in `runtime_config.go`. | The control plane rebuilds the whole resource from that projection, so a field missing there is silently dropped whenever a control plane is configured: the setting parses, appears in the dashboard, and has no effect. `TestToolSettingsProjectionCarriesEveryMCPServerField` reflects over `MCPServer` and fails on an omitted field, in both directions (seed and layering) — but it covers that type only; every other mirrored type (`minimaxSettings`, ...) is still hand-mirrored. Adding the field to just `internal/config` is the `xrux`/`parallel_tool_calls` failure shape.                          |
| **Dependency direction is `cmd -> app -> {domain, infrastructure}`**, with infrastructure implementing domain contracts.                                                                                                                                                                                                | A lane that needs a new edge says so before writing it. The `internal/{logging,events,eventbus,policy,taskstate}` cross-cutting packages import zero internal packages. Source: `docs/architecture/organisation.md`.                                                                                                                                                                                                                                                                                                                                                                                               |

## The canonical gate

```bash
~/.agents/skills/high-throughput-programming/scripts/gate-evidence.sh \
  <worktree> '<scoped test cmd>' <base>
```

Run it on the host, per lane. It executes the scoped tests, then a module-wide
build (`go build ./...`) and the linter (`golangci-lint run ./...`), and emits
JSON for the harness to use as the child's structured output.

The two extra checks are required, not optional. `go test ./pkg/...` compiles
only that package, so a changed constructor signature or interface passes its own
lane and breaks the next package to merge. That shape has bitten twice here: one
lane changed a constructor's signature, passed its own gate, and failed three
packages the moment it merged; formatting and lint drifted the same way, and only
the repository gate saw it. Both failures are paid for inside the lane when the
gate runs the build and the linter, and at merge when it does not.

Gate the **candidate** before `main` moves. Check the merge candidate out
detached — or gate the lane branch — and advance the ref only on a green result:
gating after a fast-forward lands the failure on the branch of record and leaves
only fix-forward or reset as exits. The lane brief must name the linter as well,
`golangci-lint run ./<package>/...` beside the scoped tests, because a scoped
test run cannot see a complexity or style limit and `task check` is where that
failure otherwise surfaces — once it is already merged.

A copy of the script under `.pi/control-plane-run/` is that run's disposable
state and may lag the skill's version. The skill's copy is the one to trust.

## Shape a lane's test command like this

Name every package the change can reach, plus `go vet` on the same set:

```bash
go test ./internal/app/controlplane/... ./internal/domain/applystatus/... -count=1 \
  && go vet ./internal/app/controlplane/... ./internal/domain/applystatus/...
```

Real examples, from the worked example's `lanes.json`:

| Lane surface                                 | Scoped command                                                                                                                                                             |
| -------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CLI subcommands and the State Store contract | `go test ./cmd/... ./internal/infrastructure/staterpc/... -count=1 && go vet ./cmd/...`                                                                                    |
| Workflow registry and the agent worker       | `go test ./internal/domain/workflow/... ./internal/app/agentworker/... -count=1 && go vet ./internal/domain/workflow/...`                                                  |
| Config decoding and daemon boot              | `go test ./internal/infrastructure/configuration/... ./internal/app/archied/... -count=1 && go vet ./internal/infrastructure/configuration/... ./internal/app/archied/...` |

`task check` is the _serial full gate_ — fmt, proto checks, `docs:check`, vet,
lint, build, `go test ./...`, the tools tests and the dashboard tests. The merge
train owns it. Never run it in a lane, and never two at once: it saturates the
host.

"Never two at once" includes gates you did not start. More than one agent session
works in this checkout, so before the merge train gates, check who holds it:

```bash
ps -ef | grep -E '[t]ask check|[t]ask ' | grep -v grep
```

A concurrent gate is not merely slow. Both runs regenerate
`docs/data/generated/contracts.json` and `ui/dist`, so a contended result is
worthless in either direction — neither run's verdict tells you anything about
the tree. Wait for the host to be quiet and bound the wait: still held after
about twenty minutes means report the contention rather than gate anyway.

The train integrates **local** `main`. Nothing in the gate consults `origin`, and
the version stamp comes from `git describe --tags` against local tags, so
unpushed commits neither block nor invalidate a gate run — the remote can lag
and does, since the maintainer pushes when inclined.

## The agent-discovery trap

A lane worktree is a **sibling** of the repository (`/work/apps/cp-<lane>`), not a
directory inside it. pi discovers project-scope agents under `<cwd>/.pi/agents/`,
so a child launched in a lane worktree does not see them and dies with
`Unknown agent: cp-writer`.

Fix it once per worktree:

```bash
mkdir -p /work/apps/cp-<lane>/.pi
ln -sfn /work/apps/archie-core/.pi/agents /work/apps/cp-<lane>/.pi/agents
```

`.pi/` is gitignored, so the worktree stays clean and the symlink cannot reach a
commit. This costs an hour to rediscover.

## Peer sessions over intercom

A fleet here can also run as **sibling sessions** coordinated over `intercom` —
a peer per lane — rather than pi-subagents children under one orchestrator.
These facts are peer-specific, and each has cost a real run.

- **Name the channel in the brief.** A peer's knowledge of how to report lives in
  its context, not its configuration. Write "report to <peer> over intercom" as
  an instruction; a convention that is only assumed evaporates.
- **A compaction erases conventions, not just facts.** A peer that compacts
  mid-task keeps its task state and loses its transport: it reverts to
  addressing its operator, and the coordinator reads that as idleness or death.
  **Peer silence is not evidence of either** — ask for a one-line ack over the
  channel before concluding anything. Re-priming restores the channel; the
  transport itself survives compaction intact.
- **Match the task's shape to the session's window, not its strength.** A sweep
  over hundreds of items needs the largest window available; a bounded hard
  artifact (a PRD, a conformance review, a design decision) belongs in a smaller,
  reasoning-strong session. A wide task in a small window stalls the task — too
  wide is not the same as too hard, and it is the coordinator's assignment error,
  not the peer's failure.
- **Checkpoint at ~70%, never compact mid-task.** A terse ledger sent to the
  coordinator before the window fills survives; one reconstructed afterwards
  loses exactly the identifiers — bead IDs, exact counts — that made it worth
  having. Instruct every long-running peer to report and stop there.
- **One writer per resource.** Exactly one session writes the tracker. Two
  writers produce double-closes, contradictory verdicts, and counts nobody can
  reconcile. When a sweep changes hands, the successor re-derives them rather
  than inheriting the predecessor's ledger as fact.

## Traps worth carrying

- **`touch` does not invalidate a Task source.** Task hashes file _content_, so a
  stale-binary probe has to change bytes. `task build` now tracks `ui/dist/**`,
  the generated dashboard the binary embeds, which is why the merge train still
  runs `task build --force` before the gate.
- **`GATEWAY_VERSION` and `RUNTIME_VERSION` come from `git describe`**, which no
  file checksum can invalidate: an empty commit moves the stamp while `task
build` still reports the binary up to date, shipping the old value. Tracked as
  `archie-core-pdq2`.
- **The lane gate cannot see `tools/` or the contract schema.** `tools/` is
  a separate Go module that reaches `internal/` through `docsgen`, and
  `docs/data/generated/contracts.json` is generated from wire types such as
  `agentexec.Request`. A lane that adds an import to a package `tools/`
  reaches, or changes a wire type, passes its own gate and fails
  `task check` on a missing `tools/go.sum` entry or a stale schema. Such a
  lane runs `go -C tools mod tidy` and `task docs:generate` and commits both.
  Adding or editing a page under `docs/` — a PRD included — changes
  `docs/data/generated/dev-docs.json`, which gains or loses a page: regenerate it
  and commit it in the same commit, or `docs:artifact:check` fails. That failure
  lands on `main`, not in the lane, if the ref moves first.
- **Untracked files in the shared checkout are other sessions'.** prdlint
  scratch (`docs/prds/rules.jsonl`), editor state, and gate-written artifacts
  appear and vanish under a lane. Never chase, add, stage or commit them: a
  merge train's scope is its own lanes' files, and nothing else.

## codegraph answers symbol questions cheaply

The maintainer's `codegraph` MCP server indexes this repository. Prefer it to
grep or whole-file reading for "who calls X", "is this dead", "what does this
change touch": it enumerates call sites that a grep only summarises, and labels
each caller's confidence so interface fan-out is not mistaken for direct calls.

- **`pr_impact <base> <head>`** lists changed symbols with per-symbol caller and
  test counts. Read `interface_dispatch` and the inferred tests alongside
  `test_count`: a method reached through an interface reports a low direct test
  count while many indirect tests reach it, and the compact line alone invites
  the wrong conclusion.
- **It is a derived cache.** `scip-go` and tree-sitter rebuild it, and a query
  indexes the revisions it names, so a cold query is slow and losing the index
  costs a rebuild rather than data. `list_repositories` reports the indexed
  revision — compare it with the tree before trusting a default-revision answer.
- **Stagger heavy queries.** Concurrent sessions share one instance, and
  **name-based** `symbol_context` is what locks: it returns `database is locked`
  or times out, while the same symbol **by id** returns immediately. Query
  serially and retry rather than assuming a hang.
- **Report problems one line at a time** in
  `/work/apps/codegraph/data/feedback.jsonl` — one JSON object per line, keys
  `date`, `repo`, `task`, `tools`, `helped`, `missed`, `wrong`, `slow`. A raw
  newline inside a string makes the line invalid and breaks every reader; a
  multi-line entry corrupted that file once. Validate, append, never rewrite.

## Tracker conventions

`bd` is the only tracker; no markdown TODO lists, no local trackers.

```bash
bd show <id>             # before working an item — the brief may describe a tree that has moved
bd update <id> --claim
bd close <id>            # after the work lands, not when it commits
```

Bead state moves through Dolt, never through git commits on this repository. The
Dolt remote is pushed explicitly (`bd dolt push`) and never assumed to be in sync.

## Run artifacts are evidence, not documentation

`.pi/control-plane-run/` holds a run's scaffold, lane plan, prompts, scripts and
gate output. It is gitignored and disposable. Treat it as run evidence only: a
fact worth keeping goes into this skill or `docs/`, not into a
run directory the next clean deletes.

## Worked example

`references/worked-example/` is the control-plane backlog run of September 2026,
committed verbatim: the filled orchestrator prompt, four lane briefs, three
sub-prompts, and the three agent definitions with their project invariants
inlined. Read it for the level of detail a good brief carries.

Its scripts are the snapshot's, not the current ones. The user-scope skill's
`scripts/` and `templates/` are normative and win any disagreement.
