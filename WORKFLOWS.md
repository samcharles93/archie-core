# Workflow definitions

A workflow is a YAML document: an id, what it declares to whatever starts it,
and an ordered list of steps. Definitions are stored in the control plane's
`workflow-definitions` resource and edited on the dashboard's Workflows page
(the Canvas and YAML tabs edit the same document). The shipped workflows are in
`internal/domain/workflow/shipped/`.

The server is the authority. It serves the step vocabulary, including each step
type's settings schema, in the `workflow-definitions` catalog entry
(`x-step-types`), and validates every definition when it is saved. This file
describes that vocabulary; when they disagree, the catalog wins.

```yaml
id: implement
repository: required
inputs:
  pr_number: {type: number, required: true}
steps:
  - type: repo.prepare
  - id: plan
    type: agent.run
    settings:
      read_only: true
      mission: |
        Plan this change: {{ task.prompt }}
  - id: build
    type: agent.run
    settings:
      gate: repository
      mission: "Implement it following this plan: {{ steps.plan.summary }}"
  - type: repo.commit
    settings: {message: "{{ task.title }}", push: true, if_empty: complete}
  - type: repo.open-pr
    settings: {body: "{{ steps.build.summary }}"}
```

## The workflow

| Field | Type | Meaning |
|---|---|---|
| `id` | text | The workflow's name. Required, unique. |
| `steps` | list | The steps, run in order. At least one. |
| `repository` | `required` \| `optional` \| `none` | `required` (the default) clones the task's repository into a worktree. `optional` clones one when the run names a repository and uses a scratch workspace otherwise. `none` never clones; only steps that run without a repository may be used. |
| `inputs` | mapping | Named, typed values whatever starts the run must supply: `{type, required}`, where type is `string`, `number`, `bool`, `object`, `array` or `any`. Steps read them as `{{ inputs.<name> }}`. |
| `outputs` | mapping | Named, typed results the run writes once each (same shape as inputs). A required output the run never writes parks it. A calling workflow reads them. |
| `profile` | text | The container profile (`[containers.profiles]`) the run's agents use. Resolved when the task is dispatched. |
| `identity` | text | The identity every run acts as, whatever started it. Empty runs as the starter. |
| `needs` | mapping | Harness capabilities the run requires: `captures` (bool), `gate_retries` (number). Declared outputs imply `captures`. |

## A step

| Field | Type | Meaning |
|---|---|---|
| `type` | text | The step type (below). |
| `id` | text | Names the step so later steps can read its result. Unique in the workflow; letters, digits, `-` and `_`. The run records the step under this name. |
| `settings` | mapping | The step type's settings. String settings may contain references. |
| `when` | condition | Skip the step unless the condition holds. |
| `on_failure` | `park` \| `continue` | `park` (the default) stops the run for an operator. `continue` records the failure and moves on. |
| `retry` | mapping | `attempts` (1 to 10) and `backoff` (a duration such as `30s`). A failed step runs again before `on_failure` applies. |
| `parallel` | mapping | Instead of `type`: named branches, each a list of steps, run at the same time. |

### References

Any string setting may contain `{{ path }}`, replaced when the step runs:

| Path | Value |
|---|---|
| `task.title`, `task.body` | The issue or chat request. |
| `task.prompt` | The title and body framed for an agent mission. |
| `task.repository` | `owner/name`. |
| `task.kind` | What the task came from, as prose: `GitHub issue` or `chat-originated task`. |
| `task.issue`, `task.pr` | Issue and pull request numbers. |
| `task.plan` | The plan a `human.approve` step handed over (an approved PRD). |
| `task.review` | The review comments a remediation round addresses. |
| `inputs.<name>` | A declared input; an undeclared one is refused at save. |
| `steps.<id>.summary` | What an earlier step reported, e.g. an agent's finish summary or a gate's output. |
| `steps.<id>.result.<field>` | A field of an earlier agent's structured result, or an output of an earlier `workflow.call` that waits. A field the agent's `result` schema or the callee's `outputs` does not declare is refused at save. |

Only earlier steps may be referenced; a reference to a later or unknown step is
refused when the definition is saved. A value that does not exist at run time
(for instance, a step that was skipped) renders empty.

### Conditions

`when` is a reference path, written without braces:

- `steps.assess.result.fit` holds when the value is true, a non-empty string other than `false`, or a non-zero number.
- `!steps.assess.result.fit` holds when it does not.
- `steps.classify.result.workflow == tdd` and `!=` compare the value as text.

### Parallel branches

```yaml
  - id: research
    parallel:
      code: [{id: code, type: agent.run, settings: {mission: ..., read_only: true}}]
      docs: [{id: docs, type: agent.run, settings: {mission: ..., read_only: true}}]
  - type: agent.run
    settings: {mission: "{{ steps.code.summary }} {{ steps.docs.summary }}"}
```

Branches share one worktree, so only read-only `agent.run` and `workflow.call`
steps may run in them. A branch sees the steps before the parallel step and its
own earlier steps, never a sibling's. Every branch's results are visible after
it. Parallel steps do not nest, and need at least two branches.

### How a run ends

A run ends at the first step that sets an outcome: `workflow.finish`,
`workflow.handoff`, `human.approve`, `repo.open-pr`, `repo.commit` with
`if_empty: complete`, or `review.reply`. A run that reaches the end of its steps
completes, with the last step's summary as its detail. A step that fails parks
the run unless it continues on failure.

A parked run can be retried from a top-level step instead of from the start.
The steps before it are skipped and their results stay available to
references. The resume point is named by the step's id (else its type), so
give an id to any step whose type appears twice. A run with a repository
resumes only on its pushed branch: work an earlier step did not push is not
there.

## Step types

### `agent.run`

_runs without a repository._

| Setting | Type | Meaning |
|---|---|---|
| `extra_rules` | text | Appended to the agent system prompt. |
| `gate` | `repository` \| `test-failure` | Hold the agent to the repository gate before it may finish; test-failure requires the tests to fail. |
| `mission` | text | What the agent is asked to do. May reference {{ task.* }}, {{ inputs.* }} and {{ steps.<id>.* }}. |
| `protect_tests` | bool | Write-block the repository test files. |
| `read_only` | bool | Restrict the agent to read-only tools. |
| `result` | mapping | A JSON Schema object the agent must return; later steps read it as steps.<id>.result. |
| `model` | text | The model alias that runs the agent, e.g. default or fast. Empty means default. |

### `command.run`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `level` | `error` \| `warn` | error stops the run on a failing command; warn reports it and continues. |
| `run` | list of mapping | The commands to run in order; the first failure ends the step. |

### `repo.commit`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `if_empty` | `fail` \| `skip` \| `complete` | fail stops the run, skip moves on, complete closes the issue and ends the run. |
| `message` | text | The commit message. |
| `push` | bool | Push the branch after committing. |
| `reference` | `Fixes` \| `Implements` \| `Refs` | How the commit links to the task issue. |

### `repo.open-pr`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `body` | text | The pull request description. |
| `review` | bool | Review the change first; a blocking finding parks the task instead of opening the PR. |

### `repo.prepare`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `branch` | `fresh` \| `task` | fresh starts a new branch from the base; task resumes the branch this task pushed. |

### `gate.diff-rules`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `rules` | list of mapping | Patterns the change must not match. |

### `gate.diff-size`

_needs a repository._

No settings.

### `gate.repository`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `expect` | `pass` \| `test-failure` | pass requires every gate command to pass; test-failure requires the test command to fail. |
| `repair` | bool | Let the builder fix a failing gate before the step fails. |

### `forge.close-issue`

_runs without a repository._

No settings.

### `forge.comment`

_runs without a repository._

| Setting | Type | Meaning |
|---|---|---|
| `body` | text | The comment text. |
| `on` | `issue` \| `pr` | Comment on the issue or the pull request. Empty means issue. |

### `human.approve`

_runs without a repository._

| Setting | Type | Meaning |
|---|---|---|
| `plan` | text | What the human decides on; the next workflow reads it as task.plan. |
| `then` | text | The workflow the task runs once approved. |

### `review.pr`

_needs a repository._

No settings.

### `review.reply`

_needs a repository._

| Setting | Type | Meaning |
|---|---|---|
| `body` | text | The reply to the review. |

### `review.start-round`

_needs a repository._

No settings.

### `workflow.call`

_runs without a repository._

| Setting | Type | Meaning |
|---|---|---|
| `inputs` | mapping | Callee input names mapped to scalar literals or `inputs.<name>` references to caller inputs. |
| `outputs` | mapping | Callee output names mapped to `outputs.<name>` references to caller outputs; requires `wait: true`. |
| `wait` | bool | Wait for the child run to finish before continuing. |
| `workflow` | text | The workflow to start as a child run. |

The step must declare an `id`: the step's id is the key of the call it starts,
so a retry of that call site returns the child already started instead of
starting a second one.

### `workflow.finish`

_runs without a repository._

| Setting | Type | Meaning |
|---|---|---|
| `detail` | text | What the operator reads about the outcome. |
| `status` | `completed` \| `parked` \| `waiting_human` \| `declined` | How the run ends. Empty means completed. |

### `workflow.handoff`

_runs without a repository._

| Setting | Type | Meaning |
|---|---|---|
| `detail` | text | Why the task was handed off. |
| `workflow` | text | The workflow the task is requeued under. |

`command.run` commands are `{name, argv, expect_failure}`: `argv` is the
program and its arguments, and `expect_failure` requires a non-zero exit.

`gate.diff-rules` rules are `{id, level, pattern, path, exclude_path, message}`:
`level` (`error` blocks, `warn` reports) and `pattern` (an RE2 expression matched
against each added line) are required; `path` and `exclude_path` are RE2
expressions that limit which files the rule applies to.

## What starts a workflow

- **Issues**, by label: `bug` runs `tdd`, `feature` runs `feasibility`, anything
  else runs `triage`, which classifies it and hands off.
- **Event bindings**: a webhook source mapped onto a workflow and its inputs
  (Events page).
- **Playbooks**: a trigger (issue kind and labels) and actions that dispatch
  workflows.
- **Other workflows**: `workflow.call` (a child run), `workflow.handoff` (the
  same task, requeued), `human.approve` (once approved).
- **By hand**: the New task form, or Run on the Workflows page.

The canvas Start node lists the triggers of each workflow.
