# Daemon

The daemon claims and runs tasks, and turns captured events into tasks through
bindings. This page covers both.

## Task lifecycle

Authority: `docs/architecture/agent-system.md`. The status vocabulary lives in
`internal/taskstate`, which imports no internal package.

### Adding a status

1. Add the constant to `internal/taskstate/taskstate.go` and place it in
   `Terminal`, `Actions` and `Statuses`. Its value is stored, so renaming it
   later is a migration.
2. Alias it in `internal/domain/workflow/task/task.go` and
   `internal/domain/workflow/task_aliases.go`.
3. Add it to the default catalogue in `ui/src/lib/task-meta.ts`. The server
   catalogue replaces the defaults when it loads, but the defaults render first.
4. Find every hand-written status list in SQL:
   `grep -rn "'merged'" internal/infrastructure/postgres/queries`.
   `ClearTerminalTasks` must match `taskstate.Terminal`, which
   `TestClearTerminalTasksMatchesTaskstate` enforces. The stats queries count
   outcomes by name; decide whether the new status counts.
5. Extend the table in `taskstate_test.go`.

### Adding a task field

Follow [State Store](state-store.md) for the
column and the wire. Then decide who writes it: `UpdateTask` writes a fixed
column set, so a field set at enqueue needs its own statement (as
`StampTaskBinding` does for binding provenance and inputs).

### Changing what the agent receives

- The task brief is `container.TaskPayload`, written to
  `<workspace>/.git/task.json` by `WriteTaskJSON`. Never write it to the
  workspace root: the commit stage stages everything there.
- The run request is `taskrun.Request`. `Validate` rejects a request that
  cannot run; a new requirement goes there.
- Parking always carries a class (`taskstate.ParkTransient`,
  `ParkNeedsHuman`, `ParkTerminal`) chosen by the code that parks. Pick the one
  that says what an operator must do.
- A task with no repository (`Task.HasRepository`) runs in a scratch workspace
  with no worktree grant. Code that assumes an owner and repo must check first.

## Event automation

Authority: `docs/prds/event-automation.md` (the model: source, event type,
mapping, binding, workflow, profile) and `docs/architecture/bindings.md`
(threat model).

### Where each check lives

A binding is checked twice, and a new rule usually belongs in both places:

- **On save**, in `checkedBinding` (`internal/webui/api_binding.go`), so the
  operator sees the problem while editing. It calls the domain checks
  `binding.Validate`, `binding.CompileFilter` and `Binding.CheckWorkflow`.
- **At dispatch**, in `dispatchOneBinding` and `resolveBindingTarget`
  (`internal/daemon`), because the workflow or mapping may have changed since
  the save. A refusal is recorded with `recordDispatchFailure` and starts no
  task.

Put the rule itself in `internal/domain/binding` or
`internal/domain/workflow/task` and call it from both, rather than writing it
twice.

### Adding a binding field

1. Domain type and `Validate` in `internal/domain/binding`.
2. Column, queries, store mapping, wire field, `newColumns`
   ([State Store](state-store.md)).
3. The request type and `checkedBinding` in `api_binding.go`.
4. The draft, payload and editor in `ui/src/bindings/`
   ([Frontend UI](frontend-ui.md)).
5. Its effect at dispatch, with a case in
   `internal/daemon/dispatch_inputs_test.go` or `dispatch_bindings_test.go`.

### Rules that must hold

- Signing belongs to the source. Intake verifies the HMAC against the source's
  secret before the payload is parsed.
- An event must be identified as an event type before anything dispatches it,
  and a binding applies only to its mapping's event type. Dispatch test
  fixtures therefore create an event type before inserting captures.
- The dispatch ledger is claimed before the task is enqueued (at most once).
- Anything an event supplies that selects what the agent acts on, such as a
  repository, must be checked against configuration. An event never widens
  what an agent can reach.
