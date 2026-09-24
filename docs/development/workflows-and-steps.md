# Workflows and steps

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

## Changing the definition format

- The fields a caller needs without the engine (inputs, repository, profile)
  live in `task.WorkflowInterface`, because the dashboard process must not
  import the workflow engine (`cmd/archie-ui/architecture_test.go`). Anything a
  binding or the dashboard checks goes there.
- The YAML decoder rejects unknown keys. Existing stored definitions must keep
  parsing, so a new key is optional with a default that preserves today's
  behaviour.
- Examples in `examples/workflows/` are parsed against the registered
  vocabulary by `TestExampleWorkflowsParse`. Add one for a new capability.
