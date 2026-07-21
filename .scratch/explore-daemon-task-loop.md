# daemon-task-loop

Here is the full report.

---

## 1. Full Lifecycle: GitHub Issue to Completed Task

The lifecycle spans four subsystems: **forge** (GitHub polling), **store** (SQLite persistence), **daemon** (orchestration loop), and **workflow** (pipeline engine). Here is the exact call chain.

### Phase 1 -- Discovery (polling)

**Entry:** `Daemon.Run()` at `daemon.go:72`
- Creates a `time.NewTicker` from `d.Cfg.PollInterval` (default 60s, line 259 of config.go).
- Calls `d.Cycle(ctx)` on every tick.

**`Daemon.Cycle()`** at `daemon.go:88` does three discovery activities, then drains the queue:

1. **`d.poll(ctx)`** -- line 89. Iterates over `d.Cfg.Repos`, calling `d.pollIssues(ctx, repo)` (line 140).
2. **`d.reconcilePRs(ctx)`** -- line 90. Finds tasks at `StatusPROpen` and checks GitHub for merged/closed status.
3. **`d.checkWaiting(ctx)`** -- line 91. Finds tasks at `StatusWaitingHuman` and LLM-judges human replies.

**`Daemon.pollIssues()`** at `daemon.go:140` routes by `d.Cfg.Dispatch.Trigger`:
- `"assignee"`: calls `d.Forge.AssignedIssues(ctx, owner, repo, botUser)`.
- `"label"`: calls `d.Forge.IssuesWithLabel(ctx, owner, repo, label)`.
- `"either"`: calls `pollEither()` (line 163), which unions both lists deduped by issue number.

### Phase 2 -- Enqueue

**`Daemon.poll()`** at `daemon.go:111-133`: for each discovered `forge.Issue`, calls:
- `d.Store.EnqueueIssue(ctx, owner, repo, number, title, body, labels)` -- this is an `INSERT ... ON CONFLICT DO NOTHING` at `store.go:115-118`. Returns `true` if a new row was inserted (idempotency via the `UNIQUE(owner, repo, issue_number)` constraint at line 99).
- On insert: posts an emoji reaction via `d.Forge.React()` (default "eyes", config line 319), sets the "queued" state label, and emits a `KindTaskQueued` event.
- On duplicate (inserted==false): calls `d.maybeRetryParked()` (line 134) which checks whether a human removed the `archie:parked` label and if so calls `d.Store.Requeue()`.

### Phase 3 -- Claim (queue drain)

Back in `Daemon.Cycle()` at lines 92-106, an inner `for` loop:
1. Calls `d.Store.ClaimNext(ctx)` (line 96).
2. If nil, returns (queue empty).
3. Otherwise calls `d.process(ctx, task)` (line 104), then loops again.

**`Store.ClaimNext()`** at `store.go:129-141`: one atomic SQL statement:
```sql
UPDATE tasks SET status='running', attempt=attempt+1, updated_at=datetime('now')
WHERE id = (SELECT id FROM tasks WHERE status='queued' ORDER BY id LIMIT 1)
RETURNING id, owner, repo, issue_number, title, body, labels, status,
    workflow, stage, branch, plan, notes, pr_number, tokens_used,
    iterations, attempt, park_reason
```
Because SQLite locks the database file for the duration of the UPDATE, no two claimed tasks can overlap -- the daemon is effectively single-consumer. The `RETURNING` clause returns the full row.

### Phase 4 -- Process

**`Daemon.process()`** at `daemon.go:356`:
1. Looks up the `config.Repo` for the task via `d.repoFor(task)` (line 358). Fails/tasks that belong to a now-unconfigured repo go to `StatusParked`.
2. Routes to a workflow: `wf := workflow.Route(task, d.Workflows)` (line 362).
3. Sets the "working" state label on the issue.
4. Calls `workflow.Run(ctx, wf, taskContext)` (line 365).

### Phase 5 -- Workflow Execution

**`workflow.Route()`** at `workflow.go:103-141` decides the workflow:
1. If `t.Workflow` is non-empty (set by a prior `Requeue`), use that exact workflow from the registry.
2. Otherwise inspect labels: `"bug"` -> `"tdd"`, `"feature"` -> `"feasibility"`, `"bootstrap"` -> `"bootstrap"`.
3. Falls back to `"implement"`, then `"default"`, then a hardcoded failing error workflow.

**`workflow.Run()`** at `workflow.go:145-185` executes stages sequentially:
1. Sets `t.Workflow = wf.Name`, persists via `tc.Store.Update()`.
2. For each stage: sets `t.Stage`, persists, emits `KindStageStart`, runs `stage.Run(ctx, tc)`, emits `KindStageFinish` with timing data.
3. On stage error: if `ctx.Err()` is set (daemon shutting down), returns cleanly; otherwise calls `park()` at line 216, which transitions to `StatusParked`, posts a comment, and emits `KindParked`.
4. If `tc.Outcome.Status` is non-empty after a stage, calls `finish()` at line 187, which transitions to that terminal status.

### Phase 6 -- Terminal States

A workflow terminates through `finish()` (`workflow.go:187`) or `park()` (`workflow.go:216`):

| Outcome | Status | What happens |
|---|---|---|
| `park()` | `parked` | Worktree kept, park-reason comment posted, label set. Human can remove `archie:parked` to trigger `maybeRetryParked()`. |
| `finish()` with `StatusPROpen` | `pr_open` | PR is open on GitHub. Subsequent `Cycle()` passes call `reconcilePRs()` which checks merged/closed. |
| `finish()` with `StatusWaitingHuman` | `waiting_human` | Daemon watches for human replies via `checkWaiting()`, LLM-judges approve/reject/unclear. |
| `finish()` with `StatusMerged` | `merged` | Final -- PR merged, labels cleared. |
| `finish()` with `StatusClosedWontDo` | `closed_wont_do` | Issue closed on GitHub. |
| `finish()` with `StatusRejected` | `rejected` | PR closed without merge. |

### Crash Recovery

`Daemon.Startup()` at line 52 calls `d.Store.RecoverStale(ctx)` (`store.go:234`) which runs:
```sql
UPDATE tasks SET status='queued' WHERE status='running'
```
This re-queues any task that was `running` when the previous daemon crashed. There is no distributed locking -- the single SQLite file is the authority.

---

## 2. Event Struct and Bus

### `events.Event` at `events.go:31-42`

```go
type Event struct {
    ID       int64          `json:"id,omitempty"` // set by the store sink
    At       time.Time      `json:"at"`
    Kind     string         `json:"kind"`
    TaskID   int64          `json:"task_id,omitempty"`
    Repo     string         `json:"repo,omitempty"`
    Issue    int            `json:"issue,omitempty"`
    Workflow string         `json:"workflow,omitempty"`
    Stage    string         `json:"stage,omitempty"`
    Detail   string         `json:"detail,omitempty"`
    Data     map[string]any `json:"data,omitempty"`
}
```

**Kind constants** at `events.go:16-26`:
- `KindTaskQueued`, `KindStageStart`, `KindStageFinish` (data: `duration_ms`, `error`), `KindAgentFinish` (data: `status`, `stop_reason`, `tokens`, `iterations`, `model`), `KindParked` (data: `reason`), `KindOutcome` (data: `status`, `detail`), `KindPRMerged`, `KindPRRejected`, `KindLog` (data: `level`, `msg`).

Events are intentionally the single wire type -- every consumer is just another subscriber.

### `events.Bus` at `events.go:59-128`

A simple in-process fan-out bus:

- **`Subscribe(buffer int) *Sub`** (line 68): creates a buffered channel (default 64), appends to `b.subs`. Returns a `*Sub` whose `.C` field is the read-only receive channel.
- **`Publish(e Event)`** (line 86): stamps `At` if zero, then iterates all subscribers and does a non-blocking send (`select` with `default`). If a subscriber's buffer is full, the event is dropped and `s.dropped.Add(1)` is incremented. **This is the critical design property: backpressure from a slow consumer never blocks the publisher.**
- **`unsubscribe(sub *Sub)`** (line 104): removes the sub from the slice, closes its channel.
- **`Close()`** (line 117): sets `closed` flag, closes all subscriber channels, nils the slice.
- **Bounded per-subscriber buffers** with drop-on-overflow, as documented at line 6: "a stalled dashboard connection can never apply backpressure to the task engine."

### Publish Side

The daemon has a convenience method `emit()` at `daemon.go:44-49`:
```go
func (d *Daemon) emit(e events.Event) {
    if d.Bus != nil { d.Bus.Publish(e) }
}
```
Called explicitly at every lifecycle transition -- see `daemon.go:128` (queued), `daemon.go:207` (retried), `daemon.go:237` (PR merged), `daemon.go:248` (PR rejected), `daemon.go:294` (human approved), `daemon.go:303` (human rejected).

The workflow engine also publishes via `TaskContext.Emit()` at `workflow.go:61-75`, which stamps the task identity and delegates to `tc.Bus.Publish()`.

### Subscribe Side (Consumers)

The bus is consumed by at least two subscriber patterns visible in the codebase:

1. **SQLite event log** (`internal/store/events.go`): some goroutine must subscribe and call `s.InsertEvent()` to persist each event. (The subscription setup is in the composition root `cmd/archied`. I did not read that file, but `InsertEvent` at `events.go:32` clearly exists for this purpose.)

2. **SSE fan-out** for the dashboard: the bus is designed so each HTTP SSE connection gets its own `Sub` with a small buffer, and the drop-on-overload behavior lets the dashboard fall behind without slowing the daemon.

---

## 3. ClaimNext -- SQL, Locking, Concurrency

### The SQL

At `store.go:129-141`:
```sql
UPDATE tasks SET status='running', attempt=attempt+1, updated_at=datetime('now')
WHERE id = (SELECT id FROM tasks WHERE status='queued' ORDER BY id LIMIT 1)
RETURNING id, owner, repo, issue_number, title, body, labels, status,
    workflow, stage, branch, plan, notes, pr_number, tokens_used,
    iterations, attempt, park_reason
```

Three operations in one statement:
- **Subquery**: selects the oldest queued task (`ORDER BY id LIMIT 1`).
- **UPDATE**: atomically marks it `running`, increments `attempt`.
- **RETURNING**: returns the full row, scanned by `scanTask()` (line 143).

### Concurrency Model

- **Single-process, single-threaded consumers.** SQLite's locking means only one writer at a time. The daemon has a single `Store` backed by a `*sql.DB` with `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)` (line 61). In WAL mode, readers do not block writers, but `UPDATE` still acquires a write lock -- so two concurrent `ClaimNext` calls would serialize on the database lock.

- **No distributed claims.** There is no advisory lock, no `SELECT ... FOR UPDATE`, no external coordination. The claim is just the SQL mutation + the WAL busy_timeout (5s). If two daemon processes somehow connected to the same SQLite file, one would get an error or timeout on concurrent UPDATE -- not a correctness issue, but not a supported deployment.

- **Crash recovery, not fencing.** Instead of distributed locking, `Startup()` calls `RecoverStale()` on boot (`store.go:235`): `UPDATE tasks SET status='queued' WHERE status='running'`. This re-queues any task that was in-flight when the previous process died.

- **Single-consumer queue semantics.** `Daemon.Cycle()` at line 92 loops: claim, process, claim, process. There is no parallelism within one daemon instance -- `process()` is synchronous. The `for` returns when the queue is empty (ClaimNext returns nil).

---

## 4. forge.Issue to store.Task Mapping

### `forge.Issue` at `forge.go:10-15`

```go
type Issue struct {
    Number int
    Title  string
    Body   string
    Labels []string
}
```

A pure value type -- no methods, no identity. It represents one GitHub issue at poll time.

### `store.Task` at `store.go:30-52`

```go
type Task struct {
    ID            int64   // autoincrement PK
    Owner         string  // GitHub owner
    Repo          string  // GitHub repo
    IssueNumber   int     // matches Issue.Number
    Title         string  // matches Issue.Title
    Body          string  // matches Issue.Body
    Labels        string  // comma-separated from Issue.Labels
    Status        string  // lifecycle: queued, running, waiting_human, ...
    Workflow      string  // routed workflow name
    Stage         string  // current stage name
    Branch        string  // worktree branch
    Plan          string  // PRD / implementation plan
    Notes         string  // appended agent notes
    PRNumber      int     // opened PR number
    TokensUsed    int
    Iterations    int
    Attempt       int
    ParkReason    string
    WatchCommentID int64  // human-reply anchor
}
```

### Mapping at Enqueue Time

In `Daemon.poll()` at `daemon.go:112-113`:
```go
inserted, err := d.Store.EnqueueIssue(ctx,
    repo.Owner, repo.Name, is.Number, is.Title, is.Body, strings.Join(is.Labels, ","))
```

The forge-side `[]string` labels are joined into a comma-separated string. The FORGE-side structure is flat (Number, Title, Body, Labels) -- it is the STORE that adds status tracking, routing, and operational fields. The `UNIQUE(owner, repo, issue_number)` constraint at `store.go:99` is the idempotency key: re-polling the same issue does not create a duplicate task.

### Mapping at Enqueue SQL

`store.go:115-118`:
```sql
INSERT INTO tasks (owner, repo, issue_number, title, body, labels)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(owner, repo, issue_number) DO NOTHING
```

Only 6 of 18 Task fields are set at creation. The remaining fields (status defaults to `'queued'`, workflow/stage/branch etc. default to `''` or `0`) are filled in during the processing lifecycle.

---

## 5. What Changes If ClaimNext Is Replaced with NATS JetStream?

### Current Architecture
```
[Ticker] -> Cycle() -> poll() -> [SQLite EnqueueIssue]
                    -> [SQLite ClaimNext] -> process(task) -> [workflow.Run]
```
The daemon is a single in-memory loop pulling from SQLite. It is **push-based** from SQLite's perspective (the daemon polls the DB), but effectively **single-consumer** -- no parallelism.

### Target Architecture with NATS JetStream
```
[GitHub Webhook] -> [NATS JetStream] -> [Daemon consumes from stream]
```
or
```
[Ticker] -> poll() -> [NATS JetStream Publish] 
                    -> [Daemon Subscribe] -> process(task)
```

### What Stays the Same

1. **`foreach`-over-repos polling** (`Daemon.poll()`, `daemon.go:108`). The GitHub API polling logic (`IssuesWithLabel`, `AssignedIssues`) is independent of the task queue -- you still poll GitHub the same way, you just publish the discovered issue to a NATS subject instead of INSERTing into SQLite.

2. **`workflow.Run()` and the entire workflow engine** (`workflow.go:145-185`, `steps.go`, `agent.go`, `feasibility.go`, `implement.go`, `tdd.go`). The workflow pipeline is agnostic to how tasks arrive -- it receives a `*store.Task` and a `TaskContext`. A NATS consumer would still build the same `TaskContext` and call `workflow.Run()`.

3. **`workflow.Route()`** (`workflow.go:103`). Label-to-workflow routing remains identical -- it operates on `t.Labels` and `t.Workflow`.

4. **`forge` interface and `GitHubClient`** (`forge.go`, `github.go`). The forge is only called to interact with GitHub (comment, label, PR, react) -- not as the task queue. No forge change needed.

5. **`events.Bus` and all `emit()` / `Emit()` calls.** The in-process observability bus is orthogonal to task distribution. NATS JetStream would be the task queue, not the event bus; the two are conceptually separate.

6. **Crash recovery via `RecoverStale()`** would be replaced by NATS's exactly-once delivery (with consumer sequence tracking) or at-least-once with deduplication. But the principle is the same: the daemon should be restartable without losing work.

7. **`store.Update()` and `store.Transition()`** for persisting task progress. Even with NATS as the queue, you still want an audit trail of task state. SQLite is fine for that -- it just becomes the state store rather than the queue.

### What Changes

**A. `Daemon.Run()` and `Cyc1e()` (`daemon.go:72-106`)**

Currently `Run()` has a ticker that calls `Cycle()` which does both poll-and-drain. With NATS:
- The poll ticker would still exist but would `Publish()` discovered issues to a NATS subject rather than `EnqueueIssue`.
- The drain loop would be replaced by a NATS subscription callback. The daemon would register a Pull Consumer or a Push Consumer on the stream, and `process()` would be called from the message handler, not from a `ClaimNext()` call.

Something like:
```go
func (d *Daemon) Run(ctx context.Context) error {
    sub, _ := d.JS.PullSubscribe("archie.tasks", ...)
    for {
        msgs, _ := sub.Fetch(1, ...)
        for _, msg := range msgs {
            task := deserialize(msg.Data)
            d.process(ctx, task)
            msg.Ack()
        }
    }
}
```

**B. `Store.ClaimNext()` is removed (`store.go:129`).**

The atomic SQL `UPDATE ... WHERE id = (SELECT ...)` is the heart of the single-consumer model. With NATS, the message broker provides the claim -- by delivering the message to exactly one consumer in the consumer group (JetStream's built-in `MaxDeliver` / `Ack` / `DeliveryGroup`). The entire function is replaced by `sub.Fetch()` or `sub.Consume()`.

**C. `Store.EnqueueIssue()` (`store.go:114`) becomes optional.**

You may or may not persist tasks in SQLite at enqueue time. Options:
- Keep SQLite as the system of record: the NATS subscriber writes a row on receive, then passes `*store.Task` to `process()`. This preserves the dashboard's ability to query task history without replaying the stream.
- Drop SQLite entirely at the queue level: the NATS message body carries the equivalent of `forge.Issue` + routing metadata, and the first workflow stage does the persistence.

**D. `Daemon.Startup()` crash recovery changes (`daemon.go:52`).**

`RecoverStale()` (`store.go:234`) re-queues tasks left `running`. With NATS:
- If a consumer crashes mid-process, the message is either `NACK`ed or redelivered after the `AckWait` timeout. No explicit recovery step needed.
- But: if NATS delivers a message and the daemon starts processing (updates task to `running` in SQLite), then crashes, the NATS message might already be `Ack`ed. You still need the crash-recovery query as a safety net, or you redesign to only `Ack` after the workflow completes.
- Best practice: `Ack` *after* `workflow.Run()` returns, and keep `RecoverStale` for the SQLite side.

**E. Concurrency model changes fundamentally.**

Currently one daemon processes tasks sequentially (one `ClaimNext` -> `process()` -> next `ClaimNext`). With NATS:
- You can horizontally scale: multiple daemon instances subscribe to the same JetStream consumer group, and NATS round-robins delivery.
- Within one daemon, you can parallelize: call `process()` in a goroutine per message, bounded by a semaphore. Each message has an independent worktree, so parallel workflows on different repos are safe. (Current SQLite model could also parallelize, but the synchronous `Cycle()` design prevents it.)

**F. Idempotency moves from SQL UNIQUE constraint to JetStream dedup.**

Currently `EnqueueIssue` uses `ON CONFLICT DO NOTHING` at `store.go:118`. With NATS, you would use JetStream message deduplication (`Nats-Msg-Id` header set to `owner/repo/issue_number`) to prevent re-processing the same issue enqueue.

**G. Task ordering.**

Currently FIFO via `ORDER BY id LIMIT 1`. NATS JetStream supports ordered delivery within a stream, but with multiple repos and priorities this may differ. You would need a stream per repo or use a subject hierarchy like `archie.tasks.<owner>.<repo>` to preserve ordering semantics.

### Summary Table

| Component | Current (SQLite) | NATS JetStream |
|---|---|---|
| Task queue | `tasks` table, `status='queued'` | JetStream stream |
| Claim | `UPDATE ... WHERE id = (SELECT ...) RETURNING` | `sub.Fetch()` / consumer group |
| Concurrency | Single-process, sequential | Multi-process, parallel via consumer groups |
| Idempotency | `ON CONFLICT DO NOTHING` + `UNIQUE` constraint | `Nats-Msg-Id` dedup |
| Crash recovery | `RecoverStale()` at startup | NAck/redelivery + eventual `RecoverStale` guard |
| Ordering | `ORDER BY id` (FIFO) | Stream ordering per subject |
| Daemon.Run() | `time.Ticker` -> `Cycle()` -> {poll, claim-loop} | `time.Ticker` -> poll() -> publish; sub callback -> process() |
| `Store.ClaimNext()` | **Must remove** | No equivalent needed |
| `Store.EnqueueIssue()` | **Optional** | Still useful as system-of-record |
| Workflow engine | Unchanged | Unchanged |
| Forge interface | Unchanged | Unchanged |
| events.Bus | Unchanged | Unchanged |