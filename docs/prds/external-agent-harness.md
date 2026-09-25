# External agent harnesses

**Status:** Draft
**Authority:** `docs/architecture/agent-system.md` (agent execution as a data
boundary); settles the harness non-goal deferred by
`docs/prds/service-decomposition.md`.
**Compounds with:** `docs/prds/execution-tree-state-machine.md` (a harness
run is one `agent` StepExecution).

## Decision

A workflow stage can run on an external coding-agent CLI instead of the
built-in agent loop. Supported harnesses are Claude Code, Codex, Pi, OMP and
GitHub Copilot CLI. Each authenticates with either an API key or the
operator's own subscription login, and can run with the operator's own
skills, plugins, subagents and instructions.

Archie's guarantees do not move into the harness. Git, protected paths,
read-only stages, gates and budgets are enforced by archie around the
harness process and checked against the worktree afterwards. They never
depend on the harness obeying its instructions.

## Contract

A harness is a second implementation of `agentexec.Runner`. It takes the same
`Request` and returns the same `Result`, so workflows, the daemon and the
State Store do not know which runner ran a stage.

Each adapter owns, for one CLI:

- the non-interactive invocation and its streamed JSON output;
- translating stream events into `ToolCallReporter` calls and `Usage`;
- resuming the same session with follow-up input;
- the capabilities it declares: `mcp`, `resume`, `usage`.

An adapter that cannot report usage reports zero and says so in
`StopReason`; token budgets for that stage are enforced by wall clock only.
Workflow definition validation rejects a stage whose requirements the chosen
harness does not declare, at load time, not mid-run.

## Selection

Order of precedence, first match wins:

1. the stage's `runner` in the workflow definition;
2. the agent profile's `runner`;
3. `builtin`, the ai-sdk agent loop.

`runner` names a `[harnesses.<name>]` entry. An entry with no consumer
(named by no stage and no profile) is a validation warning, and an unknown
name is a validation error.

```toml
[harnesses.claude-sub]
adapter = "claude-code"
image   = "registry.local/archie-agent-claude:latest"
home    = "claude-sam"        # a harness home, below
max_concurrent = 1

[harnesses.codex-api]
adapter = "codex"
image   = "registry.local/archie-agent-codex:latest"
api_key = { engine = "env", key = "OPENAI_API_KEY" }
max_concurrent = 4
```

## Credentials and customisation

An entry authenticates one of two ways. Setting both is a validation error.

- **API key:** `api_key` is a secret reference, injected into the container
  as the environment variable the adapter names. The key is never written
  into the worktree or a harness home.
- **Subscription:** `home` names a **harness home**, a directory under
  archie's state directory that the operator logs into once with
  `archied harness login <home>`. That command runs the CLI's own login flow
  with its config directory pointed at the home.

A harness home is also where the operator's own setup lives: skills,
plugins, subagent definitions, global instructions, and the CLI's settings
file. Archie mounts it read-write as the CLI's config directory, because
subscription tokens refresh in place. Archie never edits it. Archie's own
mission and tools reach the harness through the prompt and the invocation,
so they do not touch the operator's files.

`max_concurrent` caps simultaneous runs per entry, default 1. Runs sharing a
home share its refresh state and its subscription's rate limit; the cap is
per home, summed across entries that name it. A stage waiting for a slot is
`pending`, not `running`.

## Archie tools inside the harness

Capture tools, which are how a stage returns structured results, are served
to the harness by `archie-agent mcp`, a stdio MCP server started inside the
container and registered through the adapter's invocation flags. It serves
the stage's `CaptureTools` and the central tools the agent profile allows.
It does not serve the `workspace` toolset, for the same reason the built-in
runner withholds it.

A harness that does not declare `mcp` can only run stages with no capture
tools.

## Enforcement around the harness

The harness runs as a subprocess of `archie-agent` in the task-scoped
container, in the task worktree. Archie holds these properties by
construction and by checking the worktree after the process exits:

| Property          | Enforcement                                                                                                                                |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Model never runs git | The container has no forge credential. Archie records `HEAD` and every ref before the run; any change fails the step (`failed`, policy class). The workflow commits the stage's changes. |
| Protected paths   | A diff touching a `Protection` path fails the step.                                                                                        |
| Read-only stage   | Any worktree diff fails the step.                                                                                                          |
| Gate              | Archie runs the stage's gate after the harness exits. On failure it resumes the same session with the gate output, up to the stage's iteration budget. A harness without `resume` gets one iteration. |
| Budget            | Archie kills the process on wall-clock timeout, or when streamed usage passes the token budget.                                            |
| Cancellation      | Cancelling the step kills the process group.                                                                                               |

Harness settings that deny git or protected paths are added where an
adapter supports them, to save wasted turns. They are never the
enforcement.

## Images

The default `archie-agent` image bundles no third-party CLI. A harness entry
names an image that contains both the CLI and the `archie-agent` binary.
The repository ships one Dockerfile build target per supported adapter,
built on the `agent` image. Pull policy follows `containers.pull_policy`.

## Execution tree

A harness run is one `agent` StepExecution. Subagents the harness spawns
internally are not recorded as children. Their usage is included in the
step's `tokens_used` when the adapter reports it.

## Agents never hold a harness

An Agent reaches a harness only by invoking a Workflow whose stage names it.
No Agent tool, chat tool or capability exposes a harness, its home or its
credential. The stage boundary is what carries the container, the git and
path checks, the gate and the budget; a harness outside a stage has none of
them.

## Out of scope

- Chat through a harness. The gateway chat loop stays on the built-in
  runtime.
- Recording the harness's internal subagents as StepExecutions.
- Cost in currency.
- Choosing a harness automatically per task.

## Plan

1. Harness `Runner` with the Claude Code adapter; worktree checks for git,
   protected paths and read-only; budget and cancellation.
2. `archie-agent mcp` serving capture tools; gate-and-resume loop.
3. `[harnesses.*]` config, selection precedence and definition validation.
4. API key credentials; harness homes and `archied harness login`;
   per-home concurrency.
5. Codex adapter, then Pi, OMP and GitHub Copilot CLI.
6. Dockerfile targets per adapter.

## Verification

- The same workflow definition completes on `builtin` and on each adapter
  with the same `Result` shape and captures.
- A harness instructed to `git commit` fails its step, and the task branch
  is unchanged.
- A harness edit to a protected test file fails the step in a TDD fix stage.
- A read-only feasibility stage that writes a file fails.
- A stage with a failing gate resumes the same session with the gate output,
  then succeeds or exhausts its iterations.
- Two tasks naming the same home with `max_concurrent = 1` run one at a
  time; the second waits as `pending`.
- An API key never appears in the worktree, the harness home, the task log,
  or the event stream.
- Stopping a run kills the harness process within the step's cancel path.
