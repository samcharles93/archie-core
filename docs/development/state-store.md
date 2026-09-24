# State Store

Authority: `docs/prds/state-store-contract.md` and the State Store section of
`CLAUDE.md`. All persistence is PostgreSQL behind the standalone State Store
process; nothing else opens the database.

## Adding a column

1. Write the next migration in `internal/infrastructure/postgres/migrations/`
   (`00NN_name.sql`, goose Up and Down). Give the column a `NOT NULL DEFAULT`
   that is correct for existing rows.
2. Update the queries in `internal/infrastructure/postgres/queries/` and
   regenerate with sqlc. A query that lists columns (`ListTaskSummaries`, the
   binding selects) does not pick the new one up; `SELECT *` queries do.
3. Map it in the store (`taskFromRow`, `bindingValue` and friends). If the
   column holds encoded data, the package that owns the type owns the encoding
   (`task.EncodeInputs`), and the store and the wire both use it.
4. Add the column to `newColumns` in
   `internal/infrastructure/legacyimport/load.go` if legacy data has no value
   for it. The importer refuses a target column it cannot fill otherwise.
5. Carry it over the wire (next section) if any process other than the State
   Store reads it.

## Adding a field to a wire message

1. Add it to `proto/state/v1/state.proto` with a new field number. Never reuse
   a number; retire one with `deprecated = true`.
2. `task proto:generate`, then map it both ways in
   `internal/infrastructure/staterpc/values.go`.
3. `TestTaskProtoCarriesEveryDomainField` fills every `task.Task` field and
   fails on one the mapping forgets. If it cannot fill the field's kind, extend
   it. Other messages have no such test, so add a round trip to
   `conformance_test.go`.

## Adding an RPC or a store method

1. Put the Go method on the narrow consumer interface in
   `internal/domain/storecontract` (keep each at eight methods or fewer) and add
   the matching RPC. A Go method without an RPC does not cross the process.
2. Implement it in the Postgres store, in `staterpc.Client` (with its `var _`
   assertion) and in `staterpc.server`. A missing dependency answers
   `codes.Unavailable`, never a fabricated default.
3. Wire the dependency in every composition that serves or consumes it:
   `stateStoreDeps` in `internal/app/archied/state_store.go`, the daemon's
   assertions in `bootstrap.go`, and `internal/app/archieui/compose.go` for the
   dashboard process.
4. Task-scoped tokens may call only `Update`, `Transition` and `InsertEvent` on
   their own task (`staterpc.TaskGrants`). A new RPC is admin-only unless it is
   added there deliberately.
5. Add it to `conformance_test.go`, which runs every surface over a real gRPC
   connection.

## Adding an error sentinel

Add the sentinel to `storecontract`, a row to `wireErrors` in `values.go`, and
a case to `error_test.go`. The canonical message is part of the wire contract:
changing it breaks `errors.Is` on the client silently.
