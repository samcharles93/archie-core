# Archie Design Decisions

Record of architectural choices, reversals, and lessons learned during
development. Each entry has a date, a decision, the reasoning, and the
context that led to it. This document is the first thing to read before
changing anything that contradicts a prior decision.

---

## 2026-07-23  --  Forge-agnostic issue creation via CreateIssue

**Decision**: Add `CreateIssue(ctx, owner, repo, title, body, labels)
(int, error)` to the `forge.Forge` interface. Every forge backend
(Gitea, GitHub, future beads) implements it.

**Reasoning**: Agents discover out-of-scope findings during planning
and building. These should be filed as tickets immediately rather than
lost. Making `CreateIssue` part of the Forge interface means the agent
calls one method regardless of the underlying ticketing system.

**Future**: A `beads` forge backend will implement `CreateIssue` by
calling `bd create`. This makes beads the internal/default issue tracker
while Gitea/GitHub are external forges. The agent doesn't know which
it's talking to  --  the daemon resolves the forge from repo config.

---

## 2026-07-23  --  Dolt and Beads for agent-native issue tracking

**Decision**: Install dolt (version-controlled SQL) and beads (`bd`,
a graph-based issue tracker for AI agents) as the foundation for
multi-repo scaling and multi-agent coordination.

**Reasoning**: Beads provides persistent memory across sessions, first-
class dependency tracking, JSON output for programmatic use, and hash-
based IDs that prevent merge conflicts in multi-agent workflows. It's
built on Dolt which gives cell-level merge, native branching, and
remote sync  --  the same primitives git gives code but applied to issues.

**Context**: Gas Town (`~/gt/`) is cloned for reference  --  it implements
a multi-agent workspace on top of beads. The archie-core scaling
strategy will follow a similar model: a coordinator daemon with per-repo
agents, all sharing a beads-backed task graph.

**Current state**: dolt 2.2.2, beads 1.1.0 installed. Beads initialized
in archie-core (`.beads/`). Six issues seeded for the scaling roadmap.

---

## 2026-07-23  --  Token budgets must be generous, not predictive

**Decision**: Set `max_tokens` to 100,000,000 (100M). Do not attempt to
predict what a task needs without data. Lower limits later based on
actual per-workflow/per-repo metrics.

**Reasoning**: An agent killed mid-task at 1M tokens wastes every token
it already consumed. There is no compaction or checkpointing yet, so a
killed agent is a total loss. Hermes regularly uses 160+ steps and
hundreds of thousands of tokens per request. Large repos (tau at 70K+
lines) need proportionally more.

**Context**: The planner burned 357K tokens just to discover a fix
already existed. The builder hit 1M at step 25 of 90. Neither task was
unreasonable  --  they were just exploring a large codebase.

**Future**: Track actual token consumption per workflow/stage/repo. Use
that data to set informed limits. Build compaction or checkpointing so
a killed agent can resume rather than restart.

---

## 2026-07-23  --  baseline-fix: auto-repair pre-existing gate failures

**Decision**: When `StageBaselineGate` detects a red gate (gofumpt, go
vet, etc. failing on clean main), launch the builder to fix the
failures via TDD rather than parking. Only park if the builder cannot
fix them.

**Reasoning**: A red baseline means the repo was broken before archie
touched it. Parking creates an infinite loop  --  the next retry hits the
same failure. The agent should fix pre-existing issues, not fail on them.

**Implementation**: `StageBaselineGate` now calls `tc.Agent.Run()` with
a TDD mission when the gate fails. Uses the same builder agent. Note:
this required wiring `tc.Agent` (the runner) through the stage context.

---

## 2026-07-23  --  No hardcoded MaxSteps in workflow stages

**Decision**: Remove all hardcoded `MaxSteps` values from workflow
stages. Every stage inherits from `[budgets].max_steps` in the config.

**Reasoning**: Hardcoded values (15 for plan, 15 for TDD analyse, 15
for feasibility assess, 20 for PRD) were arbitrary and broke on large
repos. The config should be the single source of truth.

**Files affected**: `implement.go`, `tdd.go`, `feasibility.go`.

---

## 2026-07-23  --  No-op detection: close issues when build has 0 changes

**Decision**: When the builder completes with `StatusPassed` and 0 file
changes, close the issue with a comment rather than parking on "worktree
has no changes to commit".

**Reasoning**: A builder that passes with 0 changes means the fix
already exists or the issue is a no-op. The old behavior parked in an
infinite loop (commit-push fails because nothing changed, task re-queues,
builder runs again, same result).

**Implementation**: `TaskContext.BuildNoChanges` is set in the build
stage's `OnResult` when `Status == Passed && len(Changes) == 0`.
`StageCommitPush` checks this flag and calls `Forge.CloseIssue()` +
sets `Outcome = Merged` to stop the workflow.

**Test**: `TestStageCommitPushClosesIssueWhenBuildNoChanges` in
`implement_test.go`.

---

## 2026-07-23  --  assignee trigger over label trigger

**Decision**: Default to `trigger = "assignee"` instead of `trigger =
"label"`. The daemon only claims issues explicitly assigned to the bot
user.

**Reasoning**: Codex created 80+ issues with the `archie` label, and
the daemon auto-claimed every one. Label-based triggering is too
promiscuous for repos shared between humans and agents.

---

## 2026-07-23  --  DeepSeek v4 Pro as primary model

**Decision**: Use `deepseek/deepseek-v4-pro` as the planner and builder
model. Qwythos-9B (local llama.cpp) is the fallback for offline/cheap
tasks.

**Reasoning**: Qwythos couldn't complete the plan stage for tau  --  0
iterations, timed_out. DeepSeek completed it in 21 iterations. Large
repos need a larger model.

**Provider setup**: `class = "deepseek"`, API key via `DEEPSEEK_API_KEY`
env var from Bitwarden Secrets Manager (bws).

---

## 2026-07-22  --  v1.0.0 tagged

**Decision**: Tagged `v1.0.0` at commit incorporating the daemon/agent
split, NATS JetStream, Docker sandbox, Yaegi plugins, agentskills.io
skills, and Gitea forge support.

---

## 2026-07-22  --  Docker Compose stack with NATS + containers

**Decision**: The production deployment uses `docker-compose.yml` with
NATS, archied, and archie-agent containers. Config at `config.docker.toml`.

**Key fixes**:
- NATS healthcheck uses `wget localhost:8222/healthz` (not `nats-server check`)
- `host.docker.internal:host-gateway` extra_hosts for Linux
- Config mount path: `config.docker.toml` → `/etc/archie/config.toml`
- No stale `image:` tag on archied service (forces local build)

---

## 2026-07-22  --  Worktree Prepare before Container Acquire

**Decision**: Clone the worktree (`Trees.Prepare()`) before acquiring
a Docker container. Made `Prepare()` idempotent  --  skips if `.git`
already exists.

**Reasoning**: Docker bind-mounts fail with "bind source path does not
exist" if the worktree hasn't been cloned yet. The old code acquired
the container first, then the prepare stage cloned into a path that
didn't exist on the host.

---

## 2026-07-22  --  Gofumpt and Go tools must be on PATH

**Decision**: The daemon's `exec.Command` for gate checks inherits the
process PATH. `~/go/bin` (where `gofumpt`, `golangci-lint`, etc. live)
must be in the PATH before starting the daemon.

**Symptom**: `baseline red  --  gofumpt -w . fails` but manual run passes.
Root cause: `which gofumpt` returns nothing because `~/go/bin` isn't on
PATH in Hermes sessions.
