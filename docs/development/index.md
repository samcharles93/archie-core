# Development guides

Checklists for changing Archie. Each page covers one kind of change and names
every place that change must reach, so a feature that works in one layer does
not silently stop at the next. The pages point at the documents that own the
design (`docs/architecture/`, the PRDs, `CLAUDE.md`) rather than restating
them.

| Changing | Guide |
| --- | --- |
| A setting in `config.toml` or the dashboard | [Configuration settings](config-settings.md) |
| A stored field, table, RPC or error | [State Store and persistence](state-store-and-persistence.md) |
| Task statuses, fields or the task brief | [Task lifecycle](task-lifecycle.md) |
| Workflow definitions or step types | [Workflows and steps](workflows-and-steps.md) |
| Sources, event types, mappings or bindings | [Event automation](event-automation.md) |
| Anything under `ui/` | [Dashboard](dashboard.md) |
| The agent container, its image or its tools | [Agent execution and containers](agent-execution-and-containers.md) |
| A process, plugin engine, channel or NATS subject | [Processes and wiring](processes-and-wiring.md) |
| Merging a lane or a branch | [Merging work](merging-work.md) |

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
- A change to `docs/prds/` or `docs/architecture/` carries the regenerated
  `docs/data/generated/dev-docs.json`, and PRD edits follow
  `docs/prds/RULES.md` and pass `task docs:prds`.
