# archied  --  design notes

Archie is a resident daemon that keeps personal projects moving: it polls GitHub for issues labelled
`archie`, works each one in an isolated fresh-clone worktree through a routed **workflow**, and opens a
pull request for human review. Sam reviews and merges; the machine grinds in between.

Guiding principle: **environmental constraints over prompt instructions.** Agents don't get asked nicely
to write good code  --  the quality gate (vet/build/test/lint, configurable per repo and per workflow stage)
refuses to let a run succeed until it passes. The gate mechanics live in `ai-sdk/agentloop`; this repo
owns orchestration only.

## Workflows, not a pipeline

`internal/workflow` is the engine: a Workflow is an ordered list of stages over a shared TaskContext;
stages are deterministic steps (worktree, gate, diff cap, PR, comments  --  the shared step library in
`steps.go`) or agent stages (`agentloop.Run` with a stage-specific mission/toolset/gate). Routing is
label-driven (`bug` → tdd, `feature` → feasibility, default → implement), refinable by an LLM triage
stage. Adding a workflow must never require touching the engine or reimplementing steps.

Planned workflows:
- **bootstrap** (registered): deterministic marker-file PR proving invites/clone/push/PR plumbing.
- **implement**: planner (read-only agentloop, plan posted to the issue) → builder (full toolset,
  repo gate) → diff cap → PR.
- **tdd** (bugfixes): analyse → problem surface → write failing repro tests (gate stage with
  `ExpectFailure`) → fix (gate: suite passes) → PR.
- **feasibility** (features): read-only analysis → roadmap fit → won't-do close with reasons, or
  plan + PRD → email Sam → `waiting_human` → LLM-judged reply → hand off to implement.

## Task lifecycle

`queued → running(workflow:stage) → waiting_human | pr_open → merged | parked | rejected | closed_wont_do`

SQLite (`internal/store`), WAL, one row per issue with a transitions audit table. Crash recovery:
`running` tasks re-queue on startup with attempt++. Parks are never silent  --  every park comments on the
issue with the reason and gate output.

## Rejected alternative: building on tau's peer-agent runtime

tau has a working headless child (`internal/app/child.go RunChild()`, stdio-JSONL, shared SQLite): the
wrong substrate here. Its model is ephemeral child processes under an explicit "no resident process"
principle, and it drags tau's store/coordinator/session machinery into a batch worker. archied instead
shares code with tau at the module level: `ai-sdk/toolkit` (the promoted tool set) and `ai-sdk/agentloop`
(gate/budgets/loop-breaker, patterns harvested from tau's coordinator).

## Security posture

- Fine-grained PAT on the bot account only (Contents RW, PRs RW, Issues RW, Metadata R), via
  `ARCHIE_GITHUB_TOKEN`; git auth through an askpass helper so the token stays out of argv and
  `.git/config`.
- The model never runs git: `internal/worktree` owns all repo operations deterministically.
- Agent tools are jailed to the task worktree (toolkit path jail); intended deployment is a dedicated
  Incus container so the blast radius is environmental, not prompt-negotiated.
