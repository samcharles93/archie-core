# Development guides

Checklists for changing Archie, one page per area. Each names every place a
change must reach, so a feature that works in one layer does not silently stop
at the next. A change that spans areas starts on the page for the area it
begins in, which points to the others. The pages point at the documents that
own the design (`docs/architecture/`, the PRDs, `CLAUDE.md`) rather than
restating them.

| Area                                           | Covers                                                                           |
| ---------------------------------------------- | -------------------------------------------------------------------------------- |
| [Frontend UI](frontend-ui.md)                  | Anything under `ui/`, and the dashboard's API routes                             |
| [Agent](agent.md)                              | The agent container, its image, profiles, tools and workspace                    |
| [Workflows](workflows.md)                      | Workflow definitions and step types                                              |
| [Daemon](daemon.md)                            | Task statuses, fields and the brief; sources, event types, mappings and bindings |
| [State Store](state-store.md)                  | Stored fields, tables, RPCs and errors                                           |
| [Control plane and settings](control-plane.md) | Settings in `config.toml` or the dashboard                                       |
| [Processes and wiring](wiring.md)              | Where code goes, processes, plugin engines, channels, NATS subjects              |
| [Merging work](merging-work.md)                | Merging a lane or a branch                                                       |

## Every change

- Write the failing test first for anything with logic, branching or a
  contract, and check it fails on an assertion, not a compile error.
- Test through the path production uses. A test that sets a value directly on
  the consumer proves nothing about the layers that normally deliver it.
- Run `task check` and let its exit status decide the commit:
  `task check && git commit ...`. `task test` passes `-short`, which skips the
  process and Postgres tests, so run `go test -count=1 ./cmd/... ./internal/...`
  too when a change crosses a process or the database.
- Regenerate, never hand-edit, generated output: `task proto:generate`,
  `go -C tools tool sqlc generate -f $PWD/internal/infrastructure/postgres/sqlc.yaml`,
  `task ui`, `task docs:artifact`. `task proto:check` compares against the git
  index, so stage regenerated contracts before running it.
- Stage by path, or read `git status` before `git add -A`. Tests that start
  embedded NATS or write logs must use `t.TempDir()`; anything they leave in the
  tree gets committed. `task check` also adds linter-tool lines to `go.sum`
  (bead 1e37); restore `go.sum` before committing unless you changed a
  dependency.
- A change to `docs/prds/`, `docs/architecture/` **or `docs/development/`**
  carries the regenerated `docs/data/generated/dev-docs.json`: run
  `task docs:artifact` (not `task docs:check`, which checks contracts only).
  PRD edits also follow `docs/prds/RULES.md` and pass `task docs:prds`.
