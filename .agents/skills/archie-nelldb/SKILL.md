---
name: archie-nelldb
description: >-
  Maintain, debug, review, or migrate Archie's current NellDB-backed
  persistence. Load this skill when touching internal/nell, store.TaskStore,
  db_path, the tasks/events/sessions/messages collections, persisted task or
  event fields, document IDs, _rev or HLC conflicts, scans and ordering,
  restart recovery, SQLite-to-NellDB compatibility, backfills, or the pinned
  NellDB dependency; also load it when tasks, events, identities, or chat
  history disappear, duplicate, reorder, fail to transition, or fail to
  survive restart.
---

# Archie NellDB

Treat Archie's database as domain state behind an application adapter. Do not
treat it as a generic JSON map, a SQLite file with a new driver, or proof that
the target persistence architecture already exists.

## Route before acting

Use this skill for NellDB mechanics and Archie's current adapter.

Do **not** use it alone to:

| Need | Load instead or as well |
| --- | --- |
| Define general Archie terms such as Identity, Agent, Message, or WorkflowExecution | `archie-domain-reference` |
| Find every caller, data path, generated consumer, or dead adapter | `archie-codebase-discovery` |
| Decide the target repository, state machine, event model, or migration boundary | `archie-architecture-planning-campaign` and `archie-architecture-contract` |
| Add or reinterpret `db_path`, `bot_user`, or another setting | `archie-config-and-flags` |
| Triage a running daemon, damaged deployment, or missing volume | `archie-debugging-playbook` and `archie-run-and-operate` |
| Accept a storage change, migration, or dependency upgrade | `archie-validation-and-qa` and `archie-change-control` |

Stop and route to architecture planning when the requested change would define
atomic state-plus-event persistence, WorkflowExecution storage, identity-aware
keys, session/message migration, or a new process boundary. Those are OPEN
migration decisions, not adapter cleanup.

## Use the vocabulary precisely

| Term | Meaning in this repository |
| --- | --- |
| `store.TaskStore` | Archie's current application-facing task, query, event, and lifecycle interface in `internal/store/interface.go`. |
| NellDB engine | The pinned `github.com/samcharles93/NellDB` `Store` layer: records, collections, HLC ordering, conflict resolution, and storage backends. |
| `LogStore` | NellDB's persistent append-only, Zstd-framed implementation opened by `logstore.OpenLog`. |
| `sdk.DocDB` | NellDB's document adapter over one named collection. It maps `sdk.Doc` values to engine records and maintains `_rev` bookkeeping. |
| Collection | A namespace in the engine. The engine key is `collection + ":" + record ID`. |
| Document ID | The application key stored in reserved field `_id`; it is distinct from `store.Task.ID`. |
| Revision (`_rev`) | An SDK MVCC token of form `<generation>-<sha1>`. It detects an explicitly stale local document write; it is not a domain version. |
| HLC | NellDB's hybrid logical clock: Unix milliseconds plus a logical counter, used for engine ordering. |
| Node ID | The writer string stamped into `UpdatedBy`. Archie currently passes `cfg.BotUser`; `LogStore` uses the value supplied at each open. |
| LWW | Engine-level last-write-wins: higher HLC wins; equal HLC uses lexically larger `UpdatedBy`. |
| Tombstone | A record with `Deleted=true`. Normal `Get`/`List` hides it, but it remains in the append-only log until compaction. |
| Backfill | An explicit, restartable conversion of already-persisted data. Adding a decoder default is compatibility, not a backfill. |

Never transfer SQL transactions, constraints, `ORDER BY`, WAL, row counts, or
index behavior to NellDB by analogy.

## Establish CURRENT production truth

The following snapshot was verified on **2026-07-28**:

```text
config.LoadOverlay
  -> cfg.DBPath and cfg.BotUser
  -> nell.OpenStore
  -> logstore.OpenLog
  -> one underlying NellDB Store
       -> "tasks" DocDB
       -> "events" DocDB
       -> "sessions" DocDB when Telegram is configured
       -> "messages" DocDB when Telegram is configured
```

| Fact | Current evidence | Consequence |
| --- | --- | --- |
| `cmd/archied/main.go` unconditionally opens `nell.OpenStore(cfg.DBPath, cfg.BotUser)`. | `cmd/archied/main.go` | NellDB is the production task/event backend. There is no runtime backend selector. |
| `internal/store.Store` still implements `TaskStore` using SQLite. | `internal/store/{interface,store,events}.go` | SQLite remains a test/legacy implementation, not a production fallback. |
| The same engine is lent to `gateway.NewSessionStore` only inside Telegram wiring. | `cmd/archied/main.go`; `internal/gateway/session_store.go` | Tasks/events always use NellDB; session/message collections exist only when that path writes them. |
| `go.mod` pins `github.com/samcharles93/NellDB v0.2.5` with no `replace`. | `go.mod`; `go.sum` | Review behavior against the published v0.2.5 source, not a local Nell checkout. |
| Archie does not call NellDB replication, compaction, changes-feed, or vector-search APIs. | No production matches for those APIs | Do not claim those engine features are active Archie behavior. |
| Several comments and older architecture documents still say SQLite. | `cmd/archied/main.go`; `ARCHITECTURE.md` | Follow production construction and code, not stale storage nouns in prose. |

Verify the dependency without network access:

```bash
go list -m -f '{{.Path}} {{.Version}}{{if .Replace}} => {{.Replace.Path}} {{.Replace.Version}}{{end}}' github.com/samcharles93/NellDB
rg -n '^replace .*NellDB|github.com/samcharles93/NellDB' go.mod go.sum
go list -m -f '{{.Dir}}' github.com/samcharles93/NellDB
```

Expected first-line result on this snapshot:

```text
github.com/samcharles93/NellDB v0.2.5
```

If a `=> /local/path` suffix appears, stop. Commit `07fb291` removed exactly
that failure mode after a local replace made the CI Docker build unable to
resolve `/work/apps/nell-engine` and left deployment stale.

## Separate engine guarantees from Archie guarantees

| Concern | NellDB v0.2.5 proves | Archie currently adds or fails to add |
| --- | --- | --- |
| Concurrency | `MemoryStore`, `LogStore`, and `DocDB` protect individual calls with mutexes. | `Adapter.mu` serializes selected multi-call operations in one adapter instance only. It is not a file lock or distributed transaction. |
| Document conflicts | Supplying stale `_rev` to `DocDB.Put` returns `sdk.ErrConflict`. Omitting `_rev` continues from the SDK's current local revision. | Adapter helpers usually re-read a current document before `Put`; this can avoid a conflict while still copying stale application fields. |
| Cross-node conflict | Engine `Put` applies HLC LWW, then lexical `UpdatedBy` tie-break. | No Archie replication path is wired. Do not present LWW as an enforced task state machine. |
| Persistence | Default `OpenLog` appends a frame and flushes the buffered writer on each write. `Close` flushes again. | Archie defers one adapter close. No application checkpoint, snapshot, or compaction service is wired. |
| Crash behavior | Replay rebuilds the in-memory index. An incomplete tail frame is ignored. | `Daemon.Startup` separately calls `RecoverStale`, changing every `running` task to `queued`. |
| Power loss | Neither default flush nor group commit calls `fsync`. | Archie adds no explicit sync. Do not promise power-loss durability. |
| Deletes | `Remove` writes a tombstone; ordinary list/get operations hide it. | `ClearTerminalTasks` tombstones terminal task documents. File space is not reclaimed because Archie never calls compaction. |
| Queries | The engine indexes collection membership and HLC changes. `Query` is a collection-list stub; vector search is a linear scan. | The adapter performs field queries by collection scans. Gateway message search uses substring matching, not NellDB vector search. |
| Listing | `AllDocs` returns a snapshot filtered after `Store.List`; range results are not sorted by key. | Task/event methods sort when their contract needs it. Session/message code currently assumes ordering in places where the SDK does not guarantee it. |
| Node identity | Records retain their `UpdatedBy`; conflict resolution uses it. | `LogStore.NodeID()` returns the value passed to the current open. Changing `bot_user` changes future writer identity. |

Do not open the same log file from multiple processes or adapter instances and
assume `Adapter.mu` coordinates them. No cross-process lock or supported
multi-writer assembly is proven in Archie or the pinned `LogStore`.

## Know the stored shapes

### Collections and keys

| Collection | Document ID | Application data | Read pattern |
| --- | --- | --- | --- |
| `tasks` | `<owner>:<repo>:<issue_number>` | Current `store.Task` fields plus `created_at` | Direct `Get` by forge coordinates; scans for numeric task ID, status, claims, counts, and stats |
| `events` | `event:<numeric-id>` | `events.Event`; `data` is stored as a JSON string | Full scan, application filter, then explicit ID sort |
| `sessions` | `SessionContext.SessionID` | platform, bot, channel, optional thread, timestamps | Direct `Get`; full scan for list/channel lookup |
| `messages` | `<session-id>:<20-digit-sequence>` | session, sequence, sender, text, channel/thread, timestamp | Collection scan plus key-range filter |

Each `DocDB` may also contain SDK IDs `meta:clock` and `meta:vector`. Archie adds
`meta:task_id_counter` and `meta:event_id_counter`. The adapters skip IDs with
the `meta:` prefix.

Do not create an application ID beginning with `meta:`. As written,
`isMetaKey` will also hide a legitimate task whose composite key begins
`meta:` from scan-based claims and queries, even though direct `Get` can find
it. Treat this as a CURRENT risk requiring a failing test before repair.

### Task document mapping

| Group | Fields |
| --- | --- |
| Reserved and local identity | `_id`, SDK-managed `_rev`, numeric `id`, `created_at` |
| Source snapshot | `owner`, `repo`, `issue_number`, `title`, `body`, `labels`, `source`, `identity` |
| Lifecycle | `status`, `attempt`, `retry_count`, `park_reason`, `watch_comment_id` |
| Workflow/output | `workflow`, `stage`, `branch`, `plan`, `notes`, `pr_number`, `tokens_used`, `iterations` |

Apply these conversion rules:

- Store numeric fields as `int64`. `intField` accepts `float64`, `int64`,
  `int`, and `json.Number` because JSON document round trips may yield
  `float64`.
- Treat a missing `source` as `forge`; `sourceOrDefault` preserves old
  documents.
- Preserve `_id` and `_rev` by starting updates from `DocDB.Get`, not a fresh
  map.
- Do not expect `created_at` in `store.Task`; `docToTask` currently discards it,
  and the Nell document has no `updated_at`.
- Keep workflow `Update` restricted to its documented mutable subset. Do not
  silently let it rewrite identity, source coordinates, status, attempt, or
  retry state.

CURRENT defect: `EnqueueIssue` accepts `identity` but does not put `identity`
into the Nell document. Forge tasks therefore read back with an empty identity.
`EnqueueChatTask` does write both `source=chat` and `identity`, and subsequent
helpers preserve those existing fields. Do not quote target identity
documentation as proof that forge identity round-trips.

### Event document mapping

`InsertEvent` stores:

```text
id, at, kind, task_id, repo, issue, workflow, stage, detail, data
```

It clips `detail` to 4000 bytes without splitting a UTF-8 rune. It serializes
`data` to a JSON string and substitutes a `marshal_error` object when
marshalling fails. `docToEvent` ignores invalid timestamps and invalid `data`
JSON rather than returning conversion errors.

Events are observability records, not atomic task-transition history. Nell
`Transition` ignores `detail`, and the event sink writes asynchronously through
the event bus. Never claim task state and its event were committed together.

## Audit lifecycle and atomicity before editing

| Operation | Current serialization | Important limit |
| --- | --- | --- |
| `ClaimNext`, `ClaimByIssue` | Entire scan/get plus task write under `Adapter.mu` | Atomic only against operations using the same mutex on the same adapter instance. |
| Task/event ID allocation | Counter read-modify-write under `Adapter.mu` | Enqueue's duplicate check is outside the critical section; a duplicate concurrent enqueue is not proven idempotent. |
| `RecoverStale`, `ClearTerminalTasks` | Full scan and writes under `Adapter.mu` | `Transition`, `Update`, and `Requeue` do not take this mutex. |
| `Transition` | Find by numeric ID, set `Status`, put | Ignores `from` and `detail`; writes no transition record; has no adapter lock. |
| `Requeue` | Find, mutate several fields, put | Ignores `fromStatus`; writes no transition record; has no adapter lock. |
| `Update` | Get by composite key, replace allowed fields, put | One document write, but no domain state/event transaction. |
| `IncrementRetryCount` | Find, increment, put | Multi-call read-modify-write without adapter lock. |
| `InsertEvent` | ID allocation and put under `Adapter.mu` | Separate from the state mutation that caused the event. |

The method name `Transition` does not make a state machine. A correct target
must compare expected state/version, enforce legal transitions, and commit the
authoritative domain event with state at one proven atomic boundary. Design
that through the architecture campaign; do not patch only one caller.

## Investigate without damaging the log

Start read-only:

```bash
rg -n 'OpenStore|NewSessionStore|TaskStore|DBPath' cmd/archied internal
rg -n 'func \\(a \\*Adapter\\)|sdk\\.New|AllDocs|FieldID|isMetaKey' internal/nell/adapter.go
rg -n 'SaveMessage|RecentMessages|SearchMessages|sdk\\.New' internal/gateway/session_store.go
rg -n 'store\\.Open\\(|nell\\.OpenStore\\(' --glob '*.go'
```

Inspect a configured file without opening it through either database:

```bash
# Assign from the approved effective config before running
: "${DB_PATH:?set DB_PATH from the running config's db_path field}"
test -r "$DB_PATH" || { echo "DB_PATH is not readable: $DB_PATH" >&2; exit 1; }
test -f "$DB_PATH" || { echo "DB_PATH is not a regular file: $DB_PATH" >&2; exit 1; }
file -- "$DB_PATH"
od -An -tc -N16 -- "$DB_PATH"
stat -- "$DB_PATH"
```

Never run `sqlite3 "$DB_PATH"` merely because the name ends in `.db`. A current
NellDB file is a Zstd-framed binary append log. Conversely, never pass a legacy
SQLite file to `nell.OpenStore`; there is no format detector or automatic
migration.

Use focused executable evidence:

```bash
go test ./internal/nell -run '^(TestEnqueueIdempotent|TestChatTaskLifecyclePreservesRouting|TestPersistenceAcrossRestart|TestConcurrentEnqueueAndClaim)$' -count=1
go test -race ./internal/nell ./internal/gateway -count=1
go test ./internal/store ./internal/nell -count=1
go test ./cmd/archied -run '^TestChatTaskCommandsEndToEnd$' -count=1
```

As verified on 2026-07-28, those focused commands pass in an environment with
writable Go caches. If the workspace points Go temporary files at a read-only
directory, rerun without changing repository files:

```bash
mkdir -p /tmp/archie-nelldb-gotmp /tmp/archie-nelldb-gocache
env GOTMPDIR=/tmp/archie-nelldb-gotmp GOCACHE=/tmp/archie-nelldb-gocache go test -race ./internal/nell ./internal/gateway -count=1
```

`internal/storerpc` tests also require a local NATS listener. A panic saying the
embedded NATS server could not start is environment evidence until reproduced
where listener creation is allowed.

## Change persisted data safely

Follow this checklist for every field, key, collection, or dependency change:

- [ ] Name the domain owner and distinguish CURRENT behavior from APPROVED
  TARGET and OPEN migration work.
- [ ] Trace every producer, converter, reader, projection, RPC payload, JSON
  consumer, test backend, and production constructor.
- [ ] State what the new data supersedes and when the old field/path is deleted.
- [ ] Add a red test using an old document with the field absent.
- [ ] Add red round-trip tests for both forge and chat sources when task data is
  involved.
- [ ] Update every creation path, `docToTask`, `taskFields`, and the restricted
  `Update` set only when that operation owns the field.
- [ ] Preserve `_id` and `_rev`; never “fix” conflicts by deleting `_rev`.
- [ ] Give missing or legacy data an explicit compatibility rule. Label any
  guessed default OPEN.
- [ ] Check the SQLite implementation deliberately: schema, additive migration,
  every `SELECT`/`Scan`, query projections, and tests. Either preserve contract
  parity or document and prove a completed cutover.
- [ ] Test field preservation through claim, transition, requeue, retry,
  recovery, update, delete, close, and reopen as applicable.
- [ ] Add stale-revision, concurrent-writer, cancellation, malformed-value, and
  restart cases. A race-clean engine call does not prove a multi-call
  application operation is atomic.
- [ ] Measure scans at representative collection sizes before accepting another
  scan or claiming current scale makes it safe.
- [ ] Run the focused tests, then route final evidence through
  `archie-validation-and-qa` and `archie-change-control`.

Use a shared backend contract test suite when changing `TaskStore`. Current
tests are split between `internal/store` and `internal/nell`; there is no single
factory-driven suite proving identical semantics.

## Gate migration and backfill explicitly

There is no repository-supported SQLite-to-NellDB migration or backfill command
as of 2026-07-28. Do not invent an operator command in a runbook.

Require this sequence before any cutover:

1. Freeze the authoritative source or define a proven dual-write protocol.
2. Keep the SQLite input and NellDB output at different paths. Never convert in
   place.
3. Inventory task rows, numeric IDs, source coordinates, statuses, mutable
   fields, event IDs/timestamps/data, and SQLite transition history.
4. Decide explicitly where transition history goes; the current Nell adapter
   has no equivalent collection.
5. Include sessions/messages only if the chosen source actually contains them;
   current Nell gateway data has no SQLite equivalent in this repository.
6. Build a dry-run exporter/importer with schema/version identification,
   deterministic IDs, idempotent writes, checkpoints, and rejection reporting.
7. Compare counts by status/source/workflow plus stable content digests. Test
   close/reopen and crash-resume from every checkpoint.
8. Quiesce, copy, convert, validate through public application reads, then
   switch `db_path` once.
9. Retain the untouched source and a last-known-good binary/config until the
   observation window passes.
10. Delete the old reader or compatibility path only after production wiring,
    rollback evidence, and change control prove the cutover complete.

Treat NellDB `PutMany` as sequential engine writes, not a crash-atomic
transaction. Its SDK cache rollback does not undo records already written to
`MemoryStore` or `LogStore`.

## Triage by symptom

| Symptom | Prove first | Likely CURRENT seam |
| --- | --- | --- |
| `nell: path is required` | Trace `cfg.DBPath` and the direct caller. | `OpenStore` rejects an empty path; normal config finalization supplies a default. |
| `nell: open log: log: open ... no such file or directory` | Check the parent directory, mount, UID, and `db_path`. | `OpenStore` does not create the parent directory; unlike SQLite `store.Open`, it calls `os.OpenFile` directly. |
| Old tasks vanish after the backend switch | Inspect file signature and exact configured path; do not start another writer. | No SQLite import or format check exists. |
| Forge task identity is empty after reload | Compare enqueue arguments with the Nell document constructor. | `EnqueueIssue` currently drops `identity`; chat enqueue does not. |
| A stale or illegal transition succeeds | Read `Transition` and capture before/after state. | `from` is currently unused in both Nell and SQLite implementations. |
| State changed but no matching history/detail exists | Trace state write and event sink independently. | Nell transition history is absent; observability event persistence is asynchronous. |
| A directly addressable queued task is never claimed | Check whether its document ID begins `meta:`. | Scan paths filter every `meta:` prefix as internal. |
| Recent messages reorder or omit expected turns | Test more than one insertion order and restart. | `RecentMessages` assumes key-sorted `AllDocs`; v0.2.5 range results are not sorted. No message persistence tests exist. |
| `sdk: revision conflict` | Preserve the conflicting docs/revisions and identify both writers. | A stale explicit `_rev` was supplied. Re-read and recompute only if the domain command is safe to retry. |
| Database grows after updates/deletes | Compare file size with live document counts. | Append-only versions and tombstones accumulate; Archie wires no compaction. |
| Data survives process restart but not host power loss | Separate close/replay evidence from storage durability evidence. | Default writes flush but do not `fsync`. |
| NellDB SDK dependency tests fail on `server/webui/dist/nell.wasm` | Run Archie's focused packages and inspect the downloaded module archive. | Published v0.2.5 omits that embedded WASM asset; `go test github.com/samcharles93/NellDB/logstore` passes, while SDK tests cannot compile from the module cache. |
| A fix exists on another branch but main still fails | Inspect current source and branch containment. | Candidate or branch-only code is not live behavior. Never document it as fixed until merged and reverified. |

Escalate damaged-file handling to `archie-run-and-operate`. The pinned engine
proves incomplete-tail tolerance; it does not prove arbitrary corruption can be
repaired or that silently skipped data is acceptable.

## Provenance and maintenance

Volatile facts and focused tests in this skill were reverified on 2026-07-28.
Reverify after any change to `go.mod`, `cmd/archied/main.go`, `internal/nell`,
`internal/store`, `internal/gateway/session_store.go`, or the NellDB version.

```bash
go list -m -f '{{.Path}} {{.Version}}{{if .Replace}} => {{.Replace.Path}}{{end}}' github.com/samcharles93/NellDB
rg -n 'nell\\.OpenStore|NewSessionStore|store\\.Open\\(' cmd internal --glob '*.go'
rg -n 'func \\(a \\*Adapter\\)|sdk\\.New|AllDocs|isMetaKey|identity' internal/nell/adapter.go
go test ./internal/nell ./internal/gateway -count=1
go test -race ./internal/nell ./internal/gateway -count=1
git log --all --oneline -- go.mod internal/nell internal/store internal/gateway/session_store.go
```
