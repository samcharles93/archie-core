# Total dispatch outage: `sdk: revision conflict` on every enqueue

**Date:** 2026-08-05
**Severity:** Complete blocker — no task can be queued, no event can be recorded.
**Status:** Fixed forward on v0.3.0. Tracked as `archie-core-7ma`.

> **Correction.** An earlier draft of this document framed the cause as a
> long-standing namespace squat that had always been broken. It is a
> **regression**: commit `44f914c` bumped NellDB v0.2.5 → v0.3.0 touching only
> `go.mod`/`go.sum`, and `internal/nell` was never migrated. v0.2.5's
> `reindex()` indexed every record, so the read-modify-write worked; v0.3.0
> added the `isInternalID` skip. The prefix choice was always unwise, but it
> was not a defect until the bump.

## Symptom

Every poll cycle, forever, surviving restarts:

```
INFO  issue queued            repo=sam/archie-core issue=8
ERROR event sink insert failed  err="sdk: revision conflict"
ERROR nats enqueue failed       err="sdk: revision conflict"   (x3, redelivered)
```

The same issue is re-queued every 60s because it is never successfully
written, so the next poll rediscovers it as new. `processNATSTask`
(`internal/daemon/daemon.go:600`) calls `msg.Nak()` on the error, so NATS
redelivers indefinitely — which is why the three failures repeat rather than
drain. No work is dispatched at all.

## Root cause

Archie stores its auto-increment counters under keys the NellDB SDK has
reserved for itself.

`internal/nell/adapter.go:946,952`:

```go
return a.nextCounter(ctx, a.tasks,  "meta:task_id_counter")
return a.nextCounter(ctx, a.events, "meta:event_id_counter")
```

The SDK treats **any** id beginning `meta:` as its own bookkeeping
(`sdk/replicate.go:839-843`):

```go
// isInternalID reports whether an id is one of the SDK's own bookkeeping
// records (meta:clock, meta:vector) that should not be replicated.
func isInternalID(id string) bool {
	return len(id) >= 5 && id[:5] == "meta:"
}
```

`DocDB.reindex()` rebuilds the in-memory revision cache on open and **skips
every internal id** (`sdk/docdb.go:128-137`):

```go
if isInternalID(rec.ID) {
	continue          // no entry written to d.revs
}
```

`DocDB.putIn` then rejects a document that carries a `_rev` for which the
cache has no entry (`sdk/docdb.go:213-217`):

```go
curRev, exists := d.revs[nell.CompositeKey(collection, id)]
...
if incomingRev != "" && !exists {
	return "", ErrConflict
}
```

### The sequence

1. **Fresh database.** `nextCounter` does `Get` → `ErrNotFound` → creates the
   doc with no `_rev` → `Put` succeeds and caches the new rev in memory.
2. **While the process stays up**, read-modify-write works: the rev is in
   `d.revs`, so `Get`'s `_rev` matches.
3. **Restart.** `sdk.New` → `reindex()` → `ListAll` returns the counter record,
   `isInternalID("meta:task_id_counter")` is true, the record is skipped, and
   **no rev is cached**.
4. **Next counter increment.** `Get` returns the persisted doc *including* its
   `_rev`; `Put` sees `incomingRev != "" && !exists` → `ErrConflict`.

Step 4 is permanent. It affects `nextTaskID` (every `EnqueueIssue`) and
`nextEventID` (every `InsertEvent`) equally, which is exactly the pair of
errors in the log. The database is not corrupt — it is unwritable through this
code path, and reopening does not clear it.

### Reproduction

`internal/nell/counter_repro_test.go` (added, currently failing):

```
=== RUN   TestCountersSurviveReopen
    counter_repro_test.go:51: InsertEvent after reopen returned ErrConflict:
        the event log is permanently unwritable: sdk: revision conflict
--- FAIL: TestCountersSurviveReopen (0.00s)
```

Open a store, insert one event and one issue, close, reopen, insert again.
Fails in 7ms with no daemon, no NATS and no forge involved.

## Why it appeared now

Nothing in the tool changes caused it. It triggers on the **first restart after
the counter documents are created**, so any deployment that has restarted since
the NellDB store was first written is in this state. A fresh database masks it
until the next restart, which is why it can look intermittent across
environments.

## Second, latent defect

The same namespace collision silently excludes both counters from
**replication**: `isInternalID` exists precisely to keep SDK bookkeeping from
being replicated between nodes. Two archied instances share a `bot_user`-keyed
node id per `docs/architecture` and the carina deployment runs two. Their task
and event ID counters therefore never converge, so both allocate from their own
sequence and produce **colliding task IDs**. This is masked today only because
the outage above stops any allocation from succeeding.

## Fix (applied)

Pinning back to v0.2.5 was rejected: `sdk.MessageRange` is v0.3.0-only, so a
revert would drag the session and message store back with it.

**The counters moved out of the SDK's reserved namespace** —
`counter:task_id` and `counter:event_id`. This is the only change that fixes
both the outage and the replication exclusion.

That fixes the replication **exclusion**, not allocation atomicity. The
increment is still a read-modify-write serialised only by the process-local
`Adapter.mu`, so two adapters or disconnected replicas writing one log can
still allocate the same id — and for events, a `Put` over the same
`event:<id>` key would be resolved by LWW, silently dropping one. No
`sdk.Replicator` is wired today, so each log must have exactly one writing
adapter; a replicated deployment needs a distributed-atomic allocation scheme
first.

1. `internal/nell/adapter.go` — change the two keys to a namespace archie owns,
   e.g. `counter:task_id` and `counter:event_id`.

2. **Migrate on open.** Existing databases hold the old documents and the value
   in them is load-bearing: restarting the sequence at 1 would allocate IDs
   that already belong to live tasks. The old doc is still *readable* — only
   `Put` fails — so migration is a read of `next_id` from `meta:*` and a create
   of the new key, once, in `newAdapter`/`OpenStore`. The old document is left
   in place; it is inert and rewriting it is what fails.

3. `isMetaKey` became `isReservedKey` and skips the counters too, so the 13
   scan sites (`Tasks`, `findTaskByID`, `StatusCounts`, `WorkflowStats`,
   `StageStats`, `TokensByDay`, …) do not parse one as a task. The counter ids
   are matched **exactly, not by prefix**: task keys are
   `<owner>:<repo>:<number>`, so a `counter:` prefix rule would also match every
   task in a repository owned by `counter` — enqueue would report success and
   the task would then be invisible to `ClaimNext` and never run.
   `internal/gateway/session_store.go` keeps its own copy; that store writes no
   counters, so it is unaffected.

4. Migration failure is **fatal at `OpenStore`**. Swallowing it would start a
   fresh sequence from 1 and reissue IDs belonging to live tasks — two tasks
   answering to one `/approve` is worse than refusing to start.

5. Tests in `internal/nell/counter_repro_test.go`: the regression (persisted log
   → write → reopen → write again), a migration test asserting a seeded sequence
   continues rather than restarting, a migration-failure test, and a
   `counter`-owned-repo test pinning the exact-match rule.

### Also fixed here

`Adapter.EnqueueIssue` accepted an `identity` argument and silently discarded
it — the document literal had no `identity` field, so every forge-polled task
was unattributed while `internal/store` (SQLite) recorded it correctly. That
broke multi-identity dispatch and any per-identity view of the queue.

### Rejected alternative

Stripping `_rev` before `Put` in `nextCounter` would make the write succeed
(the SDK continues the chain from the current local rev when `_rev` is empty)
and is a one-line change. It is the wrong fix: it leaves archie squatting on a
reserved prefix, leaves the counters excluded from replication, and converts a
loud failure into silent last-write-wins on the ID sequence.

## Unrelated but worth noting

`cmd/archied/main.go:309`, the event sink, drops an event on error:

```go
id, err := st.InsertEvent(context.Background(), e)
if err != nil {
	log.Error("event sink insert failed", "err", err)
	continue
}
```

There is no retry and no dead-letter, and `web.Broadcast(e)` never runs, so the
dashboard silently misses the event too. Small blast radius next to the outage
above — audit and UI rather than task state — but it is real loss, and unlike
the enqueue path there is no NATS redelivery to save it.
