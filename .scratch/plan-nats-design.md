# nats-design

I now have a thorough understanding of the entire codebase. Let me compile my implementation plan.

---

## Implementation Plan: Phase 1 -- NATS Task Distribution for archie-core

### Overview

This phase adds optional NATS JetStream-based task distribution to `archied`. When the `[nats]` config block is present, newly discovered issues are published to NATS subjects rather than enqueued directly into SQLite. A pull consumer in the same daemon process receives those messages, stores them in SQLite (idempotent dedup via `ON CONFLICT DO NOTHING`), claims them, and runs the workflow. When NATS is absent, the existing SQLite-only flow (ClaimNext) continues unchanged.

---

### Step 1: Add NATS Go Dependency

**File(s):** `/work/apps/archie-core/go.mod`, `/work/apps/archie-core/go.sum`

**Changes:**
- Add `github.com/nats-io/nats.go v1.42.0` (or latest stable) to `require` block
- The JetStream API is now embedded in `nats.go` (no separate `nats.go/jetstream` module since v1.33+), so only one dependency line is needed
- Run `go mod tidy` to resolve transitive deps

**Connection to existing code:** This is the only new external dependency. The current module has only `BurntSushi/toml`, `google/go-github`, `modernc.org/sqlite`, `ai-sdk`, and `traefik/yaegi`.

**Test/verification:** `go build ./...` succeeds. `go mod tidy` is clean.

---

### Step 2: Add `[nats]` Config Section

**File(s):** `/work/apps/archie-core/internal/config/config.go`, `/work/apps/archie-core/internal/config/config_test.go`, `/work/apps/archie-core/config.example.toml`

**New type in `config.go`:**
```go
// NATSConfig configures NATS JetStream for task distribution. When URL is
// empty the existing SQLite ClaimNext flow is used.
type NATSConfig struct {
    // URL is the NATS server address, e.g. "nats://localhost:4222".
    // Empty means NATS is not configured.
    URL string `toml:"url"`
    // TokenEnv optionally names an env var holding a NATS auth token/password.
    // When empty, no authentication is attempted.
    TokenEnv string `toml:"token_env"`
}
```

**New field in `Config` struct:**
```go
type Config struct {
    // ... all existing fields ...
    NATS NATSConfig `toml:"nats"`
}
```

**Validation in `Load()`:** No new validation errors needed. `cfg.NATS.URL == ""` is the disabled state. If `TokenEnv` is set but `URL` is empty, log a warning at startup (not a config error -- the token field is simply unused).

**Default behavior:** When the TOML has no `[nats]` block, `cfg.NATS.URL` remains `""` and NATS is not used. This is the nil-means-disabled semantics.

**Test in `config_test.go`:** Add `TestNATSDefaults` -- verify that (a) a config without `[nats]` has empty URL, (b) a config with `[nats]` but empty URL loads fine, (c) a config with a NATS URL parses correctly. Add to existing `TestExampleConfigLoads` so the example config stays valid.

**Config example in `config.example.toml`:**
```toml
[nats]
# url = "nats://localhost:4222"
```

---

### Step 3: Create `internal/nats` Package

**File(s) to create:** `/work/apps/archie-core/internal/nats/client.go`, `/work/apps/archie-core/internal/nats/client_test.go`, `/work/apps/archie-core/internal/nats/subjects.go`

#### 3a. Subject Constants (`subjects.go`)

```go
package nats

import "strings"

// Task subject constants. The subject encodes the workflow type so that
// future horizontal scaling can route tasks to workflow-specific consumers.
const (
    SubjectTaskBug       = "archie.task.bug"
    SubjectTaskFeature   = "archie.task.feature"
    SubjectTaskDefault   = "archie.task.default"
    SubjectTaskBootstrap = "archie.task.bootstrap"
)

// SubjectForLabels picks the NATS subject based on issue labels, following
// the same label-to-workflow mapping as workflow.Route.
//   - "bug"       -> archie.task.bug
//   - "feature"   -> archie.task.feature
//   - "bootstrap" -> archie.task.bootstrap
//   - default     -> archie.task.default
func SubjectForLabels(labels []string) string {
    for _, l := range labels {
        switch strings.TrimSpace(l) {
        case "bug":
            return SubjectTaskBug
        case "feature":
            return SubjectTaskFeature
        case "bootstrap":
            return SubjectTaskBootstrap
        }
    }
    return SubjectTaskDefault
}
```

**Tests:** Verify subject routing: `SubjectForLabels([]string{"bug"})` returns `SubjectTaskBug`; `SubjectForLabels([]string{"feature"})` returns `SubjectTaskFeature`; `SubjectForLabels([]string{"enhancement"})` returns `SubjectTaskDefault`; `SubjectForLabels([]string{"bug", "feature"})` returns `SubjectTaskBug` (first match wins, matching workflow.Route behavior).

#### 3b. Client (`client.go`)

```go
package nats

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"
    
    "github.com/nats-io/nats.go"
    "github.com/nats-io/nats.go/jetstream"
)

const (
    streamName    = "ARCHIE_TASKS"
    consumerName  = "archie-daemon"
    msgPrefix     = "archie:" // for Msg-Id dedup
    dedupWindow   = 2 * time.Minute
    pollTimeout   = 2 * time.Second // Fetch wait time before returning nil
    maxFetchCount = 1
)

// TaskMessage is the NATS payload for a discovered issue.
type TaskMessage struct {
    Owner  string `json:"owner"`
    Repo   string `json:"repo"`
    Number int    `json:"number"`
    Title  string `json:"title"`
    Body   string `json:"body"`
    Labels string `json:"labels"` // comma-separated, as seen at enqueue time
}

// Client manages the NATS connection and JetStream resources.
type Client struct {
    conn     *nats.Conn
    js       jetstream.JetStream
    stream   jetstream.Stream
    consumer jetstream.Consumer
    log      *slog.Logger
}

// Connect dials url, sets up the JetStream stream and pull consumer.
// The stream is created with WorkQueue retention (message removed after ack),
// file storage, and the subject pattern archie.task.>.
func Connect(ctx context.Context, url string, log *slog.Logger) (*Client, error) {
    nc, err := nats.Connect(url)
    if err != nil {
        return nil, fmt.Errorf("nats connect: %w", err)
    }
    js, err := jetstream.New(nc)
    if err != nil {
        nc.Close()
        return nil, fmt.Errorf("nats jetstream: %w", err)
    }
    
    // Create or update stream
    stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
        Name:              streamName,
        Subjects:          []string{"archie.task.>"},
        Storage:           jetstream.FileStorage,
        Retention:         jetstream.WorkQueuePolicy,
        MaxMsgsPerSubject: 1,       // at most one message per subject per task
        Duplicates:        dedupWindow,
    })
    if err != nil {
        nc.Close()
        return nil, fmt.Errorf("nats stream: %w", err)
    }
    
    // Create or update pull consumer
    consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
        Name:               consumerName,
        Durable:            consumerName,
        FilterSubject:      "archie.task.>",
        AckPolicy:          jetstream.AckExplicitPolicy,
        MaxDeliver:         3,       // retry 3 times before parking
        AckWait:            5 * time.Minute, // ack within 5 mins of Fetch
        InactiveThreshold:  24 * time.Hour,
    })
    if err != nil {
        nc.Close()
        return nil, fmt.Errorf("nats consumer: %w", err)
    }
    
    return &Client{conn: nc, js: js, stream: stream, consumer: consumer, log: log}, nil
}

// Close drains and closes the NATS connection.
func (c *Client) Close() { c.conn.Close() }

// PublishTask publishes a discovered issue to the appropriate NATS subject.
// The Msg-Id header enables JetStream dedup within the dedup window.
// Returns nil on success.
func (c *Client) PublishTask(ctx context.Context, owner, repo string, number int, title, body, labels string) error {
    msg := TaskMessage{
        Owner:  owner,
        Repo:   repo,
        Number: number,
        Title:  title,
        Body:   body,
        Labels: labels,
    }
    data, err := json.Marshal(msg)
    if err != nil {
        return err
    }
    
    subject := SubjectForLabels(splitLabels(labels))
    headers := nats.Header{}
    msgID := fmt.Sprintf("%s%s/%s/%d", msgPrefix, owner, repo, number)
    headers.Set("Nats-Msg-Id", msgID)
    
    publish, err := c.js.PublishMsg(ctx, &nats.Msg{
        Subject: subject,
        Data:    data,
        Header:  headers,
    })
    if err != nil {
        return fmt.Errorf("nats publish: %w", err)
    }
    if publish == nil || publish.Sequence == 0 {
        // Duplicate suppressed by Msg-Id dedup window
        return nil
    }
    return nil
}

// Fetch retrieves one task message from the pull consumer. Returns the
// message, or nil if the context is cancelled or the fetch times out.
// The caller MUST Ack or Nak the message after processing.
func (c *Client) Fetch(ctx context.Context) (jetstream.Msg, error) {
    msgs, err := c.consumer.Fetch(maxFetchCount, jetstream.FetchMaxWait(pollTimeout))
    if err != nil {
        return nil, fmt.Errorf("nats fetch: %w", err)
    }
    for msg := range msgs.Messages() {
        if msg == nil {
            continue
        }
        if err := msgs.Error(); err != nil {
            return nil, fmt.Errorf("nats fetch error: %w", err)
        }
        return msg, nil
    }
    return nil, nil // no message available
}

// DecodeTask decodes a TaskMessage from a NATS message.
func DecodeTask(msg jetstream.Msg) (TaskMessage, error) {
    var tm TaskMessage
    if err := json.Unmarshal(msg.Data(), &tm); err != nil {
        return tm, fmt.Errorf("nats decode: %w", err)
    }
    return tm, nil
}

func splitLabels(labels string) []string {
    if labels == "" {
        return nil
    }
    return strings.Split(labels, ",")
}
```

**Key design decisions in this struct:**
- `DedupWindow: 2 minutes` -- matching the typical poll interval. A restarted daemon within 2 minutes won't get dupes.
- `MaxDeliver: 3` -- retries before a message is placed on the NA pile (NATS moves it to a dead-letter state internally).
- `AckWait: 5 minutes` -- generous for long-running workflows.
- `WorkQueuePolicy` -- message removed from stream after ack.
- `FilterSubject: "archie.task.>"` -- consumer receives all task subjects.

**Tests for `client_test.go`:**

The test needs an embedded NATS server. Use `nats-server` binary or the `github.com/nats-io/nats-server/v2/server` package in test mode.

```go
func TestConnectAndPublish(t *testing.T) {
    // Start embedded NATS server on random port
    srv := startEmbeddedNATSServer(t)
    defer srv.Shutdown()
    
    url := fmt.Sprintf("nats://localhost:%d", srv.Port())
    client, err := Connect(context.Background(), url, slog.New(slog.DiscardHandler))
    if err != nil {
        t.Fatal(err)
    }
    defer client.Close()
    
    // Publish a task
    if err := client.PublishTask(ctx, "sam", "todo", 1, "fix bug", "body", "bug"); err != nil {
        t.Fatal(err)
    }
    
    // Fetch it back
    msg, err := client.Fetch(ctx)
    if err != nil {
        t.Fatal(err)
    }
    if msg == nil {
        t.Fatal("expected a message")
    }
    
    tm, err := DecodeTask(msg)
    if err != nil {
        t.Fatal(err)
    }
    if tm.Owner != "sam" || tm.Number != 1 {
        t.Fatalf("unexpected task: %+v", tm)
    }
    msg.Ack()
}

func TestDedup(t *testing.T) {
    // Publish same task twice; second publish should be deduped
    // Verify by checking PublishTask returns no error both times
    // and only one message is consumable
}
```

---

### Step 4: Add `ClaimByIssue` to Store

**File(s):** `/work/apps/archie-core/internal/store/store.go`, `/work/apps/archie-core/internal/store/store_test.go`

**Changes to `store.go`:**

Add a new method that atomically claims a task by its issue identity rather than the oldest-queued:

```go
// ClaimByIssue atomically claims a queued task by owner/repo/issue_number.
// Returns nil if the task is not in queued state (already claimed, parked,
// or completed). This is used by the NATS consumer path where the task
// was just inserted via EnqueueIssue and needs to be claimed immediately.
func (s *Store) ClaimByIssue(ctx context.Context, owner, repo string, number int) (*Task, error) {
    row := s.db.QueryRowContext(ctx, `
        UPDATE tasks SET status='running', attempt=attempt+1, updated_at=datetime('now')
        WHERE owner=? AND repo=? AND issue_number=? AND status='queued'
        RETURNING id, owner, repo, issue_number, title, body, labels, status,
            workflow, stage, branch, plan, notes, pr_number, tokens_used,
            iterations, attempt, park_reason`, owner, repo, number)
    t, err := scanTask(row)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    return t, err
}
```

**Why a new method instead of reusing ClaimNext?** Because ClaimNext claims the *oldest* `queued` row. When multiple tasks are in the queue (e.g., after a daemon restart), claiming the one we just inserted is not guaranteed. ClaimByIssue targets the exact row.

**Test in `store_test.go`:**

Add `TestClaimByIssue`:
```go
func TestClaimByIssue(t *testing.T) {
    s := openTest(t)
    ctx := context.Background()
    
    // Enqueue two tasks
    s.EnqueueIssue(ctx, "sam", "todo", 1, "t1", "", "bug")
    s.EnqueueIssue(ctx, "sam", "todo", 2, "t2", "", "feature")
    
    // Claim task 2 by issue (not the oldest)
    task, err := s.ClaimByIssue(ctx, "sam", "todo", 2)
    if err != nil || task == nil {
        t.Fatalf("ClaimByIssue = (%v, %v)", task, err)
    }
    if task.IssueNumber != 2 || task.Status != StatusRunning {
        t.Fatalf("unexpected task: %+v", task)
    }
    
    // Claiming the same issue again should return nil
    task2, err := s.ClaimByIssue(ctx, "sam", "todo", 2)
    if err != nil || task2 != nil {
        t.Fatalf("second claim must return nil, got (%v, %v)", task2, err)
    }
    
    // ClaimNext should now pick up task 1 (still queued)
    task3, _ := s.ClaimNext(ctx)
    if task3 == nil || task3.IssueNumber != 1 {
        t.Fatalf("expected task 1, got %+v", task3)
    }
}
```

---

### Step 5: Modify Daemon to Support NATS

**File(s):** `/work/apps/archie-core/internal/daemon/daemon.go`, `/work/apps/archie-core/internal/daemon/daemon_test.go`

#### 5a. Add NATS Field to Daemon

```go
import (
    // ... existing imports ...
    "github.com/samcharles93/archie-core/internal/nats"
)

type Daemon struct {
    // ... all existing fields ...
    Nats *nats.Client // nil means NATS not configured; use SQLite path
}
```

#### 5b. Modify `poll()` for NATS Publish Path

The `poll()` method needs a branch for NATS. When `d.Nats != nil`, publish to NATS instead of calling `Store.EnqueueIssue`. The ack reaction, state label setting, and event emission remain the same -- they reflect discovery, not storage.

Refactored `poll()`:
```go
func (d *Daemon) poll(ctx context.Context) {
    for _, repo := range d.Cfg.Repos {
        issues := d.pollIssues(ctx, repo)
        for _, is := range issues {
            inserted := d.tryPublish(ctx, repo, is)
            if !inserted {
                d.maybeRetryParked(ctx, repo, is)
                continue
            }
            d.Log.Info("issue queued", "repo", repo.FullName(), "issue", is.Number, "title", is.Title)
            if ack := d.Cfg.Dispatch.AckReaction; ack != "" {
                if err := d.Forge.React(ctx, repo.Owner, repo.Name, is.Number, ack); err != nil {
                    d.Log.Warn("ack reaction failed", "issue", is.Number, "err", err)
                }
            }
            d.Forge.SetStateLabel(ctx, repo.Owner, repo.Name, is.Number, d.Cfg.Dispatch.StateLabel("queued"), d.Cfg.Dispatch.LabelValues())
            d.emit(events.Event{
                Kind: events.KindTaskQueued, Repo: repo.FullName(),
                Issue: is.Number, Detail: is.Title,
            })
        }
    }
}

// tryPublish returns true if this issue was accepted (either enqueued in SQLite
// or published to NATS) for the first time. False means it was already tracked.
func (d *Daemon) tryPublish(ctx context.Context, repo config.Repo, is forge.Issue) bool {
    labels := strings.Join(is.Labels, ",")
    
    if d.Nats != nil {
        // When NATS is configured, we must check if the issue is already known
        // before publishing -- we still need maybeRetryParked to work. The
        // NATS Msg-Id dedup prevents duplicate messages but we want to detect
        // the "already tracked" case here to allow retry-parked detection.
        existing, err := d.Store.TaskByIssue(ctx, repo.Owner, repo.Name, is.Number)
        if err != nil {
            d.Log.Error("check existing failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
            return false
        }
        if existing != nil {
            return false // already tracked
        }
        
        if err := d.Nats.PublishTask(ctx, repo.Owner, repo.Name, is.Number, is.Title, is.Body, labels); err != nil {
            d.Log.Error("nats publish failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
            return false
        }
        return true
    }
    
    // SQLite path (unchanged logic, extracted for clarity)
    inserted, err := d.Store.EnqueueIssue(ctx,
        repo.Owner, repo.Name, is.Number, is.Title, is.Body, labels)
    if err != nil {
        d.Log.Error("enqueue failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
        return false
    }
    return inserted
}
```

Note: For the NATS path, we call `Store.TaskByIssue` first. This is a read-only check that only reads, never writes. The actual storage happens in the NATS consumer. This preserves the `maybeRetryParked` behavior.

#### 5c. Modify `Cycle()` for NATS Consumer

```go
func (d *Daemon) Cycle(ctx context.Context) {
    d.poll(ctx)
    d.reconcilePRs(ctx)
    d.checkWaiting(ctx)
    if d.Nats != nil {
        d.drainNATS(ctx)
    } else {
        d.drainSQLite(ctx)
    }
}

// drainSQLite processes queued tasks from SQLite (existing behavior).
func (d *Daemon) drainSQLite(ctx context.Context) {
    for {
        task, err := d.Store.ClaimNext(ctx)
        if err != nil {
            d.Log.Error("claim failed", "err", err)
            return
        }
        if task == nil {
            return
        }
        d.process(ctx, task)
    }
}

// drainNATS processes tasks from NATS, falling back to SQLite for
// requeued tasks (waiting_human, retry-parked).
func (d *Daemon) drainNATS(ctx context.Context) {
    for {
        // Try NATS first
        msg, err := d.Nats.Fetch(ctx)
        if err != nil {
            d.Log.Error("nats fetch failed", "err", err)
            return
        }
        if msg != nil {
            d.processNATSTask(ctx, msg)
            // After processing, try again immediately
            continue
        }
        
        // Fall back to SQLite for requeued tasks that came via direct
        // calls to Store.Requeue (waiting_human approval, retry-parked).
        task, err := d.Store.ClaimNext(ctx)
        if err != nil {
            d.Log.Error("sqlite claim failed", "err", err)
            return
        }
        if task == nil {
            return
        }
        d.process(ctx, task)
    }
}
```

**Important:** The dual-drain approach means newly discovered tasks come through NATS and requeued tasks (waiting_human approval, retry-parked) come through SQLite ClaimNext. This is clean and avoids needing to publish NATS messages for every requeue.

#### 5d. Add `processNATSTask`

```go
func (d *Daemon) processNATSTask(ctx context.Context, msg jetstream.Msg) {
    // Decode
    tm, err := nats.DecodeTask(msg)
    if err != nil {
        d.Log.Error("nats decode failed", "err", err)
        msg.Ack() // bad message, don't retry
        return
    }
    
    // Store in SQLite (idempotent — ON CONFLICT DO NOTHING)
    inserted, err := d.Store.EnqueueIssue(ctx, tm.Owner, tm.Repo, tm.Number, tm.Title, tm.Body, tm.Labels)
    if err != nil {
        d.Log.Error("nats enqueue failed", "err", err)
        msg.Nak()
        return
    }
    
    // Claim the task by identity
    task, err := d.Store.ClaimByIssue(ctx, tm.Owner, tm.Repo, tm.Number)
    if err != nil {
        d.Log.Error("nats claim failed", "err", err)
        msg.Nak()
        return
    }
    if task == nil {
        // Task already claimed by another consumer (multi-daemon scenario)
        // or in a terminal state. Ack and move on.
        msg.Ack()
        return
    }
    
    // Process (this sets labels, runs workflow, records outcome)
    d.process(ctx, task)
    
    // Ack after processing completes.
    // If the daemon crashes before ack, the NATS message is redelivered.
    // EnqueueIssue's idempotency + ClaimByIssue's nil-on-not-queued guard
    // prevents double-processing.
    msg.Ack()
}
```

---

### Step 6: Wire NATS in `cmd/archied/main.go`

**File(s):** `/work/apps/archie-core/cmd/archied/main.go`

**Changes:**

Add import for the nats package:
```go
"github.com/samcharles93/archie-core/internal/nats"
```

Between store opening and daemon construction, add:
```go
// NATS client (optional)
var natsClient *nats.Client
if cfg.NATS.URL != "" {
    natsClient, err = nats.Connect(ctx, cfg.NATS.URL, log)
    if err != nil {
        log.Error("nats connect failed", "err", err)
        return 1
    }
    defer natsClient.Close()
    log.Info("nats connected", "url", cfg.NATS.URL)
}
```

Add to Daemon construction:
```go
d := &daemon.Daemon{
    // ... all existing fields ...
    Nats: natsClient,
}
```

The `--once` flag path should still work: `d.Cycle(ctx)` handles both NATS and SQLite paths. When NATS is configured and `--once` is used, the Cycle does poll + drainNATS once. Since Fetch has a 2-second timeout, `drainNATS` will return after the timeout if no message is available, which is fine for the one-shot case.

---

### Step 7: Integration Verification

**Approach:** Manual end-to-end test with a local NATS server.

1. Start `nats-server` locally (or via Docker: `docker run -p 4222:4222 nats`)
2. Create a `config.toml` with:
   ```toml
   bot_user = "test-bot"
   [nats]
   url = "nats://localhost:4222"
   # ... other required fields
   ```
3. Run `archied --once`
4. Verify:
   - `nats pub` / `nats sub` shows the task subject messages
   - NATS JetStream stream `ARCHIE_TASKS` exists (`nats stream list`)
   - The task appears in SQLite after the consumer processes it
   - Workflow completes normally

**Automated integration test (future, not Phase 1):** A test that starts an embedded NATS server, creates a daemon with NATS configured, simulates an issue poll, and verifies the full cycle. Phase 1 keeps tests at the unit level.

---

### Summary of All Changes

| Step | File | Change |
|------|------|--------|
| 1 | `go.mod`, `go.sum` | Add `github.com/nats-io/nats.go` |
| 2 | `internal/config/config.go` | Add `NATSConfig` struct, `NATS` field to `Config` |
| 2 | `internal/config/config_test.go` | Test NATS config loading |
| 2 | `config.example.toml` | Add commented-out `[nats]` block |
| 3a | `internal/nats/subjects.go` | Subject constants + `SubjectForLabels` |
| 3b | `internal/nats/client.go` | `Client` struct, `Connect`, `Close`, `PublishTask`, `Fetch`, `DecodeTask` |
| 3b | `internal/nats/client_test.go` | Unit tests with embedded NATS server |
| 4 | `internal/store/store.go` | Add `ClaimByIssue` method |
| 4 | `internal/store/store_test.go` | Test `ClaimByIssue` |
| 5 | `internal/daemon/daemon.go` | Add `Nats` field, refactor `poll()`, add `drainNATS`/`drainSQLite`/`processNATSTask`, modify `Cycle()` |
| 5 | `internal/daemon/daemon_test.go` | No changes needed for Phase 1 (existing tests unchanged) |
| 6 | `cmd/archied/main.go` | Wire `nats.Connect`, pass client to Daemon |

### Design Decisions and Rationale

1. **Dual inner loop (NATS then SQLite) for Phase 1.** Rather than modifying `Requeue` to also publish NATS messages, the NATS drain falls back to ClaimNext for requeued tasks. Simple, works, and the fallback disappears in a later phase when requeue itself publishes NATS messages.

2. **Msg-Id dedup instead of SQL UNIQUE for NATS path.** The existing `EnqueueIssue` with `ON CONFLICT DO NOTHING` is the idempotency mechanism in the consumer. The NATS Msg-Id dedup is a first-line defense to avoid storing duplicate messages at all. Together they form a robust dedup chain.

3. **Workflow subject routing duplicates `Route()` logic.** The `SubjectForLabels` function mirrors `workflow.Route`'s label-to-workflow mapping. This is intentional -- the subject encodes the workflow type so future multi-daemon setups can filter by subject. The duplication is minimal (one small function) and is kept in the `nats` package to avoid a dependency on the `workflow` package.

4. **`ClaimByIssue` is a targeted query, not a general claim.** It only claims a task by the exact owner/repo/number triple and only when it's in `queued` status. This is safe for the NATS consumer where we just inserted the task. It also works as a lock: two consumers racing on the same message will see one succeed and one get nil.

5. **Three retries, 5-minute ack wait.** NATS JetStream redelivers unacked messages. With `MaxDeliver: 3`, a failing task is retried twice after the first attempt. After 3 failures, the message goes to the NA pile (JetStream's internal dead state for work queues). This is a safety net, not a permanent error -- eventually the operator checks the NATS dead-letter state or we add a dedicated handler.

6. **Ack after `process()` completes, even on error.** If the workflow parks the task, that's a successful outcome from the task distribution perspective -- the message is acked and removed from the stream. Only daemon crashes leave the message unacked.

### Potential Challenges

1. **Embedded NATS server for tests.** Go tests that need a real NATS server typically use `github.com/nats-io/nats-server/v2/server` embedded mode. This adds a test-only dependency. Alternative: use a `nats-server` binary in CI. For Phase 1, the test file should use an embedded server and note it in the build tag if the binary isn't available.

2. **`drainNATS` blocks on Fetch.** `Fetch` has a 2-second poll timeout before returning nil. When the queue is empty, the NATS drain exits (returns nil) and the Cycle ends, which is correct. The ticker in `Run()` creates a new Cycle on the next tick, so empty-queue idle detection is fine.

3. **`SubjectForLabels` vs `Route()` divergence risk.** If someone adds a new label-to-workflow mapping in `Route()`, they need to update `SubjectForLabels` too. Mitigation: keep both functions small, add a comment referencing the other, and add a cross-package test that verifies they stay in sync on the known label set.

---

### Critical Files for Implementation
- `/work/apps/archie-core/internal/daemon/daemon.go` — Core changes: poll branching, Cycle splitting, NATS consumer logic, new helper methods
- `/work/apps/archie-core/internal/config/config.go` — Add NATSConfig struct and NATS field to Config
- `/work/apps/archie-core/internal/nats/client.go` — New package: NATS connection management, publish, consume, JetStream setup
- `/work/apps/archie-core/internal/store/store.go` — Add ClaimByIssue for targeted task claiming
- `/work/apps/archie-core/cmd/archied/main.go` — Wire NATS client startup and inject into Daemon