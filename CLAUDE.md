# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**archied** is a resident daemon that polls a forge (GitHub, Gitea planned) for issues assigned/labelled to it, works each one in an isolated git worktree through a routed workflow (bootstrap / implement / TDD / feasibility), and opens pull requests for human review. See `ARCHITECTURE.md` for the full package map, workflow engine design, task lifecycle, and key design decisions — read it before making non-trivial changes, since several invariants (env-enforced gates, model never runs git, agent execution as a data boundary) aren't obvious from the code alone.

## Build & Test

Commands are defined in `Taskfile.yml` (uses [Task](https://taskfile.dev)); run `task --list` to see everything.

```bash
task build      # build archied and archie-agent binaries into bin/
task test       # go test ./... -count=1
task fmt        # gofumpt -w . && go fix ./...
task vet        # go vet ./...
task lint       # golangci-lint run ./...
task check      # fmt + go fix + vet + build + test — matches the per-repo [[repos.gate]] archied runs on itself
```

Run a single test: `go test ./internal/workflow/... -run TestName -v`.

Docker/NATS stack (multi-process split — see below): `task docker-build`, `task docker-up`, `task docker-down`, `task docker-logs`.

## Architecture

Full details live in `ARCHITECTURE.md` — do not duplicate it here; skim it whenever touching the daemon, workflow engine, or forge/worktree/store boundaries. Summary of what's evolved since that doc was last fully accurate:

- The daemon has grown an RPC split (`internal/forgerpc`, `internal/storerpc`, `internal/worktreerpc`, `internal/natsrpc`, `internal/nats`) alongside the in-process model described in ARCHITECTURE.md — check `internal/nats/` and `docker-compose.yml` for the current split between `archied` and sandboxed workers.
- `internal/gate/` — quality gate execution (the "environmental constraints over prompt rules" mechanism).
- `internal/skill/`, `internal/skillscript/`, `internal/yaegiutil/` — SKILL.md support (listed as "Planned" in ARCHITECTURE.md; now has real implementation — check these packages for current state before assuming it's unbuilt).
- `internal/channels/` — inbound chat/notification channels (e.g. Telegram — see `internal/channels/telegram/`, `internal/channels/email/`); channel-agnostic interface in `internal/channels/channel.go`.
- `internal/memory/`, `internal/nell/`, `internal/gateway/`, `internal/container/`, `internal/plugin/`, `internal/tools/`, `internal/taskrun/` — not documented in ARCHITECTURE.md; read the package before modifying, its doc comment and tests are the source of truth.

**Task lifecycle** (from ARCHITECTURE.md, still current): `queued → running(workflow:stage) → pr_open → merged|rejected`, with `waiting_human` and `parked` side states. Crash recovery re-queues anything left `running`; parks always post a comment with the reason — never fail silently.

**Config** is daemon-level TOML (see `config.example.toml`), not per-repo files in this repo. Two live instances run on a separate host (`carina`), each with a distinct `bot_user` — see the `two-archied-instances-run-on-host-carina-ssh` beads memory (`bd memories carina`) if working on multi-identity/dispatch behavior.

## Conventions

- Go 1.26.3, module `github.com/samcharles93/archie-core`.
- `ai-sdk/runtime`, `ai-sdk/agentloop`, `ai-sdk/core` are external — shared with the `tau` project at the module level, not vendored copies. Changes there affect both repos.
- Non-interactive shell commands only: some environments alias `cp`/`mv`/`rm` to `-i`. Use `cp -f`, `mv -f`, `rm -f`/`rm -rf`, `scp -o BatchMode=yes`, `ssh -o BatchMode=yes`, `apt-get -y`.
- Environmental enforcement over prompt rules: gates, test-file protection, and diff caps are code-level checks the agent cannot talk its way around — follow this pattern for any new safety constraint rather than adding it to a system prompt.
- **Plugin engine rule (strict):** “engine” means a typed, lifecycle-managed capability family with an owning registry or manager; it never means another method on the generic plugin interface. Keep `plugin.Plugin` metadata-only, give plugins narrow typed registrars instead of daemon access, and apply the lifecycle and trust-boundary requirements in [ARCHITECTURE.md#plugin-engine-rule-strict](ARCHITECTURE.md#plugin-engine-rule-strict). Its mechanically testable structure is enforced by `internal/plugin/architecture_test.go`; reviewers enforce its lifecycle, access, and trust semantics.
- The model/agent never runs git directly — worktree operations (`internal/worktree/`) are deterministic daemon-owned steps, not agent tools.

## Development Protocol (mandatory)

This is not optional style guidance — every code change in this repo follows this sequence. It mirrors what the `implement-and-verify` workflow (`.claude/workflows/implement-and-verify.js`) automates for `archie-core-abg` beads, and it's the same discipline archied enforces on *itself* via the TDD workflow in `ARCHITECTURE.md`.

**`task check` is the definitive quality gate.** `task check` = `fmt` + `go fix ./...` + `vet` + `build` + `test`, and it is the one command that must pass, clean, before any change is considered done — it's the literal analogue of the `[[repos.gate]]` archied runs against every PR it opens. Don't hand-pick a subset of `go test`/`go vet`/`go build` and call it good; run `task check` (or the scoped equivalent, e.g. `go test ./internal/foo/... -count=1` + `golangci-lint run ./internal/foo/...` for a single package) and confirm it's clean.

**Red-then-green, definitively:**
1. **Red** — write the failing test(s) first, against the *intended* behavior, before any implementation code exists. Run them and confirm they fail for the right reason (not a compile error).
2. **Green** — write the minimum implementation to make those tests pass. No implementation code is written before its test exists and fails.
3. Confirm via `task check` (or scoped `go test .../... -count=1`) that the suite is green, `golangci-lint run` reports zero findings, and `gofumpt -w .` reports no unformatted files.
4. No hardcoded values in tests (use parameterized test tables), no hardcoded identity/config values in implementation code, and every error path gets a test.

**Adversarial verification is mandatory, and it runs as a separate pass from a fresh reviewer — never self-certified by the implementer.** After red-then-green is green:
- Spawn a review with no memory of how the code was written (a fresh subagent, or `/code-review`/`/security-review` as appropriate) and instruct it to assume every line is wrong until proven correct.
- The checklist: all tests pass, lint is zero-findings, formatting is clean, then a manual read of every non-test file in the changed package for — dead code paths, unchecked error returns, hardcoded values that should be parameters/constants, interface satisfaction, nil-pointer risk, goroutine leaks, and races on shared mutable state.
- Findings are reported (use `ReportFindings` when running as a review agent), not silently fixed by the same pass that wrote the code — a finding that survives blocks closing the work, it doesn't get waved through.
- Only close out (beads: `bd close`) once the adversarial pass reports zero surviving findings.

## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
```

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists.
- Use `bd remember` / `bd memories <keyword>` for persistent knowledge — do NOT use MEMORY.md files.
- Issues live in a local Dolt DB; sync uses `refs/dolt/data` on the git remote; `.beads/issues.jsonl` is a passive export, not the source of truth. See [SYNC_CONCEPTS.md](https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md).
