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

Treat Archie's database as domain state behind an application adapter.

Stop for architecture planning when the change defines atomic
state-plus-event persistence or WorkflowExecution storage — OPEN decisions.

## Use the vocabulary precisely

| Term | Meaning in this repository |
| --- | --- |
| `store.TaskStore` | Archie's application-facing task, query, event, and lifecycle interface in `internal/store/interface.go`. |
| NellDB engine | The pinned `github.com/samcharles93/NellDB` `Store` layer: records, collections, HLC ordering, conflict resolution, and storage backends. |
| `LogStore` | NellDB's persistent append-only, Zstd-framed implementation opened by `logstore.OpenLog`. |
| `sdk.DocDB` | NellDB's document adapter over one named collection. Maps `sdk.Doc` values to engine records. |
| Collection | A namespace in the engine. Engine key is `collection + ":" + record ID`. |
| Document ID | The application key stored in reserved field `_id`; distinct from `store.Task.ID`. |
| Revision (`_rev`) | SDK MVCC token of form `<generation>-<sha1>`. Detects stale local document writes. |
| HLC | NellDB's hybrid logical clock: Unix milliseconds plus logical counter. |
| Node ID | Writer string stamped into `UpdatedBy`. Archie passes `cfg.BotUser`. |
| LWW | Engine-level last-write-wins: higher HLC wins; equal HLC uses lexically larger `UpdatedBy`. |
| Tombstone | Record with `Deleted=true`. Normal `Get`/`List` hides it; remains in append-only log until compaction. |

Never transfer SQL transactions, constraints, `ORDER BY`, WAL, row counts, or
index behavior to NellDB by analogy.

## Establish CURRENT production truth

Verified **2026-07-28**: `config.LoadOverlay` → `cfg.DBPath`/`cfg.BotUser` →
`nell.OpenStore` → `logstore.OpenLog` → one Store with `"tasks"`, `"events"`,
and optionally `"sessions"`/`"messages"` DocDBs.

| Fact | Current evidence | Consequence |
| --- | --- | --- |
| `cmd/archied/main.go` unconditionally opens `nell.OpenStore(cfg.DBPath, cfg.BotUser)`. | `cmd/archied/main.go` | NellDB is the production task/event backend. No runtime backend selector. |
| `internal/store.Store` still implements `TaskStore` using SQLite. | `internal/store/{interface,store,events}.go` | SQLite remains a test implementation; NellDB is the production backend. |
| The same engine is lent to `gateway.NewSessionStore` only inside Telegram wiring. | `cmd/archied/main.go`; `internal/gateway/session_store.go` | Session/message collections exist only when that path writes them. |
| `go.mod` pins `github.com/samcharles93/NellDB v0.2.5` with no `replace`. | `go.mod`; `go.sum` | Review against published v0.2.5 source. |
| Archie does not call NellDB replication, compaction, changes-feed, or vector-search APIs. | No production matches | Do not claim those engine features are active. |

Verify dependency:

```bash
go list -m -f '{{.Path}} {{.Version}}{{if .Replace}} => {{.Replace.Path}} {{.Replace.Version}}{{end}}' github.com/samcharles93/NellDB
rg -n '^replace .*NellDB|github.com/samcharles93/NellDB' go.mod go.sum
go list -m -f '{{.Dir}}' github.com/samcharles93/NellDB
```

Expected: `github.com/samcharles93/NellDB v0.2.5`. If a `=> /local/path` suffix
appears, stop. Commit `07fb291` removed exactly that failure mode.

## Separate engine guarantees from Archie guarantees

| Concern | NellDB v0.2.5 proves | Archie currently adds or fails to add |
| --- | --- | --- |
| Concurrency | `MemoryStore`, `LogStore`, and `DocDB` protect individual calls with mutexes. | `Adapter.mu` serializes selected multi-call operations in one adapter instance only. |
| Document conflicts | Stale `_rev` to `DocDB.Put` returns `sdk.ErrConflict`. | Adapter helpers usually re-read before `Put`; can avoid conflict while copying stale application fields. |
| Cross-node conflict | Engine `Put` applies HLC LWW, then lexical `UpdatedBy` tie-break. | No Archie replication path is wired. |
| Persistence | Default `OpenLog` appends a frame and flushes buffered writer on each write. Close flushes again. | Archie defers one adapter close. No checkpoint, snapshot, or compaction service wired. |
| Crash behavior | Replay rebuilds in-memory index. Incomplete tail frame is ignored. | `Daemon.Startup` separately calls `RecoverStale`, changing every `running` task to `queued`. |
| Power loss | Neither default flush nor group commit calls `fsync`. | Archie adds no explicit sync. |
| Deletes | `Remove` writes a tombstone; ordinary list/get hide it. | `ClearTerminalTasks` tombstones terminal task documents. File space not reclaimed (no compaction). |
| Queries | Engine indexes collection membership and HLC changes. `Query` is a collection-list stub. | Adapter performs field queries by collection scans. Gateway message search uses substring matching. |
| Listing | `AllDocs` returns a snapshot filtered after `Store.List`; range results not sorted by key. | Task/event methods sort when contract needs it. Session/message code assumes ordering where SDK does not guarantee it. |
| Node identity | Records retain their `UpdatedBy`; conflict resolution uses it. | `LogStore.NodeID()` returns value passed to current open. Changing `bot_user` changes future writer identity. |

Do not open the same log file from multiple processes or adapter instances.
No cross-process lock or supported multi-writer assembly is proven.

## Know the stored shapes

### Collections and keys

| Collection | Document ID | Application data | Read pattern |
| --- | --- | --- | --- |
| `tasks` | `<owner>:<repo>:<issue_number>` | Current `store.Task` fields plus `created_at` | Direct `Get` by forge coordinates; scans for numeric task ID, status, claims, counts, stats |
| `events` | `event:<numeric-id>` | `events.Event`; `data` stored as JSON string | Full scan, application filter, explicit ID sort |
| `sessions` | `SessionContext.SessionID` | platform, bot, channel, optional thread, timestamps | Direct `Get`; full scan for list/channel lookup |
| `messages` | `<session-id>:<20-digit-sequence>` | session, sequence, sender, text, channel/thread, timestamp | Collection scan plus key-range filter |

Each `DocDB` may also contain SDK IDs `meta:clock` and `meta:vector`. Archie adds
`meta:task_id_counter` and `meta:event_id_counter`. Adapters skip `meta:` prefix.

Do not create an application ID beginning with `meta:`. `isMetaKey` will also
hide a legitimate task whose composite key begins `meta:` from scan-based claims
and queries, even though direct `Get` can find it. Treat as CURRENT risk.

### Task document mapping

| Group | Fields |
| --- | --- |
| Reserved and local identity | `_id`, SDK-managed `_rev`, numeric `id`, `created_at` |
| Source snapshot | `owner`, `repo`, `issue_number`, `title`, `body`, `labels`, `source`, `identity` |
| Lifecycle | `status`, `attempt`, `retry_count`, `park_reason`, `watch_comment_id` |
| Workflow/output | `workflow`, `stage`, `branch`, `plan`, `notes`, `pr_number`, `tokens_used`, `iterations` |

Conversion rules:
- Store numeric fields as `int64`. `intField` accepts `float64`, `int64`,
  `int`, and `json.Number`.
- Treat missing `source` as `forge`; `sourceOrDefault` preserves old documents.
- Preserve `_id` and `_rev` by starting updates from `DocDB.Get`.
- Keep workflow `Update` restricted to its documented mutable subset.

CURRENT defect: `EnqueueIssue` accepts `identity` but does not put `identity`
into the Nell document. Forge tasks read back with empty identity.
`EnqueueChatTask` does write both `source=chat` and `identity`.

### Event document mapping

`InsertEvent` stores: `id, at, kind, task_id, repo, issue, workflow, stage,
detail, data`. Clips `detail` to 4000 bytes without splitting UTF-8 rune.
Serializes `data` to JSON string; substitutes `marshal_error` on failure.
`docToEvent` ignores invalid timestamps and invalid `data` JSON.

Events are observability records, not atomic task-transition history. Nell
`Transition` ignores `detail`, and the event sink writes asynchronously.

## Audit lifecycle and atomicity before editing

| Operation | Current serialization | Important limit |
| --- | --- | --- |
| `ClaimNext`, `ClaimByIssue` | Scan/get + write under `Adapter.mu` | Atomic only with same mutex on same instance. |
| Task/event ID allocation | Counter RMW under `Adapter.mu` | Enqueue duplicate check outside critical section. |
| `RecoverStale`, `ClearTerminalTasks` | Full scan + writes under `Adapter.mu` | `Transition`, `Update`, `Requeue` do not take mutex. |
| `Transition` | Find by ID, set `Status`, put | Ignores `from`/`detail`; writes no transition record; no lock. |
| `Requeue` | Find, mutate fields, put | Ignores `fromStatus`; writes no transition record; no lock. |
| `Update` | Get by key, replace allowed fields, put | One doc write, no domain state/event transaction. |
| `IncrementRetryCount` | Find, increment, put | Multi-call RMW without adapter lock. |
| `InsertEvent` | ID allocation + put under `Adapter.mu` | Separate from state mutation that caused event. |

`Transition` does not make a state machine. Target must compare expected
state/version, enforce legal transitions, commit state with domain event at one
atomic boundary.

## Investigate without damaging the log

```bash
rg -n 'OpenStore|NewSessionStore|TaskStore|DBPath' cmd/archied internal
rg -n 'func \\(a \\*Adapter\\)|sdk\\.New|AllDocs|FieldID|isMetaKey' internal/nell/adapter.go
rg -n 'SaveMessage|RecentMessages|SearchMessages|sdk\\.New' internal/gateway/session_store.go
rg -n 'store\\.Open\\(|nell\\.OpenStore\\(' --glob '*.go'
```

Inspect a configured file without opening it:

```bash
: "${DB_PATH:?set DB_PATH from the running config's db_path field}"
test -r "$DB_PATH" && test -f "$DB_PATH"
file -- "$DB_PATH"
od -An -tc -N16 -- "$DB_PATH"
stat -- "$DB_PATH"
```

Never run `sqlite3 "$DB_PATH"` because the name ends in `.db`. A current
NellDB file is a Zstd-framed binary append log. Never pass a legacy SQLite file
to `nell.OpenStore`; there is no format detector or automatic migration.

```bash
go test ./internal/nell -run '^(TestEnqueueIdempotent|TestChatTaskLifecyclePreservesRouting|TestPersistenceAcrossRestart|TestConcurrentEnqueueAndClaim)$' -count=1
go test -race ./internal/nell ./internal/gateway -count=1
go test ./internal/store ./internal/nell -count=1
go test ./cmd/archied -run '^TestChatTaskCommandsEndToEnd$' -count=1
```

As verified 2026-07-28, these pass with writable Go caches.

## Change persisted data safely

- Trace every producer, converter, reader, projection, RPC payload, test
  backend, and production constructor. State what new data supersedes.
- Add red tests with old/missing fields. Test round-trips for forge and chat.
- Update creation paths, `docToTask`, `taskFields`, and restricted `Update` set.
- Preserve `_id`/`_rev`; never delete `_rev` to "fix" conflicts.
- Give missing/legacy data explicit compatibility rules.
- Check SQLite implementation deliberately — contract parity or proven cutover.
- Test field preservation through all lifecycle operations and reopen.
- Add stale-revision, concurrent-writer, cancellation, malformed-value, restart cases.
- Measure scans at representative sizes before accepting another scan.
- Use shared backend contract test suite for `TaskStore` changes.

## Gate migration and backfill explicitly

There is no repository-supported SQLite-to-NellDB migration or backfill command
as of 2026-07-28.

Before any cutover:

1. Freeze authoritative source or define proven dual-write protocol.
2. Keep SQLite input and NellDB output at different paths.
3. Inventory task rows, IDs, coordinates, statuses, fields, events, transition history.
4. Decide where transition history goes; build dry-run exporter/importer with
   deterministic IDs, idempotent writes, checkpoints, rejection reporting.
5. Compare counts by status/source/workflow plus content digests.
6. Quiesce, copy, convert, validate, switch `db_path` once.
7. Retain untouched source and last-known-good binary/config until window passes.
8. Delete old reader only after production wiring, rollback, and change control.

Treat NellDB `PutMany` as sequential engine writes, not crash-atomic.
Its SDK cache rollback does not undo records already written.
## Triage by symptom

| Symptom | Prove first | Likely CURRENT seam |
| --- | --- | --- |
| `nell: path is required` | Trace `cfg.DBPath` and direct caller. | `OpenStore` rejects empty path. |
| `nell: open log: ... no such file or directory` | Check parent directory, mount, UID, `db_path`. | `OpenStore` does not create parent directory. |
| Old tasks vanish after backend switch | Inspect file signature and configured path. | No SQLite import or format check. |
| Forge task identity empty after reload | Compare enqueue args with Nell document constructor. | `EnqueueIssue` drops `identity`; chat enqueue does not. |
| Stale/illegal transition succeeds | Read `Transition`, capture before/after state. | `from` unused in both Nell and SQLite. |
| State changed but no matching history/detail | Trace state write and event sink independently. | Nell transition history absent; event persistence async. |
| Directly addressable queued task never claimed | Check if document ID begins `meta:`. | Scan paths filter every `meta:` prefix. |
| Recent messages reorder or omit turns | Test more than one insertion order and restart. | `RecentMessages` assumes key-sorted `AllDocs`; v0.2.5 not sorted. |
| `sdk: revision conflict` | Preserve conflicting docs/revisions. | Stale explicit `_rev` supplied. |
| Database grows after updates/deletes | Compare file size with document counts. | Append-only tombstones accumulate; no compaction. |
| Data survives restart but not power loss | Separate close/replay from storage durability. | Default writes flush but don't `fsync`. |
| NellDB SDK tests fail on `server/webui/dist/nell.wasm` | Run Archie's focused packages. | Published v0.2.5 omits embedded WASM asset. |
| Fix on another branch; main still fails | Inspect current source and branch containment. | Candidate code is not live behavior. |
