# Task lifecycle

Authority: `docs/architecture/agent-system.md`. The status vocabulary lives in
`internal/taskstate`, which imports no internal package.

## Adding a status

1. Add the constant to `internal/taskstate/taskstate.go` and place it in
   `Terminal`, `Actions` and `Statuses`. Its value is stored, so renaming it
   later is a migration.
2. Alias it in `internal/domain/workflow/task/task.go` and
   `internal/domain/workflow/task_aliases.go`.
3. Add it to the default catalog in `ui/src/lib/task-meta.ts`. The server
   catalog replaces the defaults when it loads, but the defaults render first.
4. Find every hand-written status list in SQL:
   `grep -rn "'merged'" internal/infrastructure/postgres/queries`.
   `ClearTerminalTasks` must match `taskstate.Terminal`, which
   `TestClearTerminalTasksMatchesTaskstate` enforces. The stats queries count
   outcomes by name; decide whether the new status counts.
5. Extend the table in `taskstate_test.go`.

## Adding a task field

Follow [State Store and persistence](state-store-and-persistence.md) for the
column and the wire. Then decide who writes it: `UpdateTask` writes a fixed
column set, so a field set at enqueue needs its own statement (as
`StampTaskBinding` does for binding provenance and inputs).

## Changing what the agent receives

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
