# Workflows

Authority: `docs/architecture/agent-system.md` for the lifecycle,
`docs/prds/event-automation.md` for inputs, repository modes and profiles.

Workflow definitions are YAML stored as a control-plane resource. The State
Store validates them and the agent compiles them, each against the step
vocabulary it registers, so both must see the same step types.

## Adding a step type

1. Write the factory in `internal/domain/workflow` as a `StepType`. It decodes
   and validates its settings and returns an error for anything it cannot
   honour. The factory runs when a definition is saved and again when it is
   compiled, so a bad definition is refused on save.
2. Register it through a provider in
   `internal/infrastructure/workflowsteps/workflowsteps.go`. That one list is
   what every process registers, which is how the validating and executing
   sides agree.
3. An agent-driven step builds on `AgentStage`; do not start a second path to
   the agent runner.
4. A step that needs no repository goes in `repoFreeStepTypes` in
   `definition.go`, or workflows declaring `repository: none` or `optional`
   cannot use it. A step that needs one may assume `tc.Dir` holds a worktree
   only when the workflow's repository is `required`.
5. A step that ends the workflow sets `tc.Outcome`. A workflow that runs out of
   steps without an outcome is parked as a definition bug.
6. Add a parse test (settings accepted and refused) and a run test with fake
   `Trees` and agent runner.

## Retiring a step type

A step word cannot simply be deleted. A stored or pinned definition is
validated as a whole collection, so one unknown word fails EVERY workflow's
definition to decode and the workflow-definitions resource becomes unreadable.
Retire a stage by removing it from its workflow's `Stages` and adding it to
`retiredSteps` in `definition.go`, which keeps the word resolving as an inert
stage. The retired stage must do nothing: the behaviour it used to perform now
belongs to whatever layer took it over (the remediate workflow's in-container
resume moved to daemon worktree preparation, `archie-core-866m`).

## Changing the definition format

- The fields a caller needs without the engine (inputs, repository, profile)
  live in `task.WorkflowInterface`, because the dashboard process must not
  import the workflow engine (`cmd/archie-ui/architecture_test.go`). Anything a
  binding or the dashboard checks goes there.
- The YAML decoder rejects unknown keys. Existing stored definitions must keep
  parsing, so a new key is optional with a default that preserves today's
  behaviour.
- A declared `outputs:` entry is written through a capture tool, so it forces
  the captures need: `task.WorkflowInterface.Needs` resolves that from the
  declaration rather than the author also writing `needs.captures`. A Kit
  profile whose harness serves no capture tools is refused before dispatch.
- Examples in `examples/workflows/` are parsed against the registered
  vocabulary by `TestExampleWorkflowsParse`. Add one for a new capability.

## Enabling and disabling

The `workflow-enablement` control-plane resource lists, per org, the workflows
that org has disabled; anything unlisted is enabled. Its readers are the
daemon's binding dispatch (a disabled workflow's binding is skipped like an
unarmed one), the dashboard's binding save (refused) and binding list
(`workflow_disabled`), and work requests. A new reader of "may this org run
this workflow" goes through `task.WorkflowEnablement.Enabled`, never a second
list. The dashboard acts in the default org until access resolves a caller's
own.

## Starting a workflow from chat

`task_spawn` accepts `workflow` and an `inputs` object. The gateway forwards
inputs through the task creation contract; it does not interpret individual
workflows. The daemon checks inputs against the pinned `WorkflowInterface`
before acquiring a container and parks invalid requests with the reason.
