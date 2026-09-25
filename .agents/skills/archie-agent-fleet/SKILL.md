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
- **Untracked files in the shared checkout are other sessions'.** prdlint
  scratch (`docs/prds/rules.jsonl`), editor state, and gate-written artifacts
  appear and vanish under a lane. Never chase, add, stage or commit them: a
  merge train's scope is its own lanes' files, and nothing else.

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
