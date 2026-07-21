# nats-integration

Here is the full analysis.

---

## Report: archie-core Codebase Exploration for NATS Integration

### 1. Current Go Version and Key Dependencies

**Go version:** `go 1.26.2` (from `/work/apps/archie-core/go.mod`, line 3)

**Direct dependencies** (from `/work/apps/archie-core/go.mod`):
- `github.com/BurntSushi/toml v1.6.0` -- TOML config parsing
- `github.com/google/go-github/v78 v78.0.0` -- GitHub API client
- `modernc.org/sqlite v1.54.0` -- pure-Go SQLite (no CGo)
- `github.com/samcharles93/ai-sdk v0.1.17` -- local LLM SDK (replaced to `/work/projects/ai-sdk`)
- `github.com/traefik/yaegi v0.16.1` -- Go interpreter for custom skill/workflow stages

**Indirect dependencies:** `go-humanize`, `go-querystring`, `uuid`, `go-isatty`, `go-strftime`, `browser`, `bigfft`, `golang.org/x/sys`, `modernc.org/libc/mathutil/memory`

**Key observation:** The dependency tree is very lean. There are zero messaging, queue, or streaming libraries. No `go.sum` entries match "nats" -- zero NATS transitive dependencies exist.

---

### 2. Full Config Struct (all fields, all structs)

File: `/work/apps/archie-core/internal/config/config.go`

```
Config (205-231)
├── WorkDir         string        `toml:"work_dir"`
├── DBPath          string        `toml:"db_path"`
├── PollInterval    Duration      `toml:"poll_interval"`  (custom Duration type, TOML strings like "60s")
├── Label           string        `toml:"label"`
├── BotUser         string        `toml:"bot_user"`
├── BotEmail        string        `toml:"bot_email"`
├── DiffCapLines    int           `toml:"diff_cap_lines"`
├── Forge           Forge         `toml:"forge"`
│   ├── Type        string        `toml:"type"`       (default: "github")
│   ├── Host        string        `toml:"host"`       (default: "https://github.com")
│   └── TokenEnv    string        `toml:"token_env"`  (default: "ARCHIE_GITHUB_TOKEN")
├── Dispatch        Dispatch      `toml:"dispatch"`
│   ├── Trigger     string        `toml:"trigger"`     ("assignee", "label", "either"; default: "assignee")
│   ├── AckReaction string        `toml:"ack_reaction"` ("eyes"; "off" disables)
│   └── Labels      map[string]string `toml:"labels"`  (keys: queued/working/waiting/pr/parked)
├── Models          map[string]string `toml:"models"`   (role -> model ref, e.g. "triage", "planner", "builder")
├── Providers       map[string]Provider `toml:"providers"`
│   ├── Class       string        `toml:"class"`       ("openai", "ollama", etc.)
│   ├── APIKeyEnv   string        `toml:"api_key_env"`
│   └── BaseURL     string        `toml:"base_url"`
├── Agent           Agent         `toml:"agent"`
│   ├── Mode        string        `toml:"mode"`        ("inprocess" or "subprocess")
│   ├── Command     string        `toml:"command"`     (executable path for subprocess mode)
│   └── Env         []string      `toml:"env"`         (extra env vars forwarded to worker)
├── Budgets         Budgets       `toml:"budgets"`
│   ├── MaxSteps    int           `toml:"max_steps"`
│   ├── MaxTokens   int           `toml:"max_tokens"`
│   ├── WallClock   Duration      `toml:"wall_clock"`
│   └── GateMaxFailures int      `toml:"gate_max_failures"`
├── Web             Web           `toml:"web"`
│   └── Listen      string        `toml:"listen"`      (default: "127.0.0.1:8484"; "off" disables)
├── Notify          Notify        `toml:"notify"`
│   └── Webhook     string        `toml:"webhook"`     (n8n webhook URL)
└── Repos           []Repo        `toml:"repos"`
    ├── Owner       string        `toml:"owner"`
    ├── Name        string        `toml:"name"`
    ├── Base        string        `toml:"base"`        (branch PRs target; default "main")
    ├── Gate        [][]string    `toml:"gate"`        (quality-gate commands)
    ├── Protect     []string      `toml:"protect"`     (path suffixes agents cannot write)
    ├── Ecosystem   string        `toml:"ecosystem"`   ("go", "python", "node", "rust", "custom")
    ├── Preflight   [][]string    `toml:"preflight"`   (override ecosystem default)
    └── TestGlob    string        `toml:"test_glob"`   (override ecosystem default)
```

The `Config` struct is loaded by `config.Load(path)` which decodes TOML, then applies defaults. The `Load()` function validates: agent mode, provider URLs, dispatch trigger values, dispatch labels, bot_user requirement, repos requirement, forge type (currently only "github"), and test glob patterns.

---

### 3. How the Project is Built/Run Today

**No Makefile, Taskfile, justfile, Dockerfile, docker-compose, or CI config files exist anywhere in the project.**

The project is built and run directly with the Go toolchain:

- **Build:** `go build ./cmd/archied` to build the daemon binary, `go build ./cmd/archie-agent` for the subprocess worker.
- **Run:** `archied -config ~/.config/archie/config.toml` (the `run()` func in `cmd/archied/main.go`).
- **Entry points:**
  - `/work/apps/archie-core/cmd/archied/main.go` -- the daemon (orchestrator)
  - `/work/apps/archie-core/cmd/archie-agent/main.go` -- standalone worker (used in subprocess mode)
- **Composition root** is `cmd/archied/main.go`. It wires: config loading, forge client creation, SQLite store, event bus, web UI server, LLM runtime, agent runner, worktree manager, workflow registry.
- **Daemon lifecycle:** `Startup()` (recovery, invitation accept, push verify) -> `Run()` (poll loop with ticker) -> each `Cycle()` does: poll issues, reconcile PRs, check waiting tasks, drain queued tasks sequentially.
- **Testing:** standard `go test ./...` -- no bespoke task runner.

---

### 4. Existing NATS or Messaging Patterns

**NATS: None whatsoever.** Zero references in Go source, zero in go.sum, zero in any config.

**Existing messaging system -- in-process event bus** (`/work/apps/archie-core/internal/events/events.go`):

This is a lightweight in-process pub/sub bus designed for observability. Key design:
- Single `Event` type carries all observability data (timestamps, kind strings, task IDs, freeform `Data map[string]any`)
- Mutex-based fan-out with bounded per-subscriber channels
- **Drops on overflow** -- a stalled dashboard client cannot backpressure the task engine
- Subscribers register via `bus.Subscribe(buffer int)` which returns a `*Sub` with a read-only `C <-chan Event`
- Event kinds: `task_queued`, `stage_start`, `stage_finish`, `agent_finish`, `parked`, `outcome`, `pr_merged`, `pr_rejected`, `log`

**How it is used** (from `cmd/archied/main.go` lines 86-107):
1. A `bus := events.NewBus()` is created in `main()`
2. A store-sink subscriber consumes all events, writes them to SQLite, then broadcasts to the web UI
3. The `webui.Server.Broadcast()` fans events to SSE clients
4. The `daemon.Daemon` has a `Bus *events.Bus` field used in `emit()` calls throughout task lifecycle

**Current architecture is single-process:** The daemon polls GitHub issues, processes them sequentially in-process (or via subprocess `archie-agent`), and all state lives in SQLite. There is no multi-worker, no job queue, no cross-process communication beyond the subprocess protocol in `internal/agentexec/`.

---

### 5. Recommended `[nats]` Config Block

Following the existing TOML conventions (see `/work/apps/archie-core/config.example.toml` and the `config.go` struct patterns), a `[nats]` block should:

- Match the two-level key style (e.g., `[forge]`, `[dispatch]`, `[web]`)
- Use lowercase with underscores for field names
- Support a `"off"` / empty-string sentinel for optional features
- Be a new struct on the `Config` type, not nested under any existing section

Based on the existing patterns, the convention-compliant config block would look like:

```toml
# NATS JetStream configuration for distributed task queue.
# Remove or comment out these sections to run in single-process mode.
[nats]
url = "nats://localhost:4222"     # NATS server URL
# token = "$NATS_TOKEN"           # or user/password for auth (use env var)
# user = "archie"
# password_env = "NATS_PASSWORD"

[nats.tls]                        # optional TLS section as a sub-table
# cert = "/path/to/cert.pem"
# key  = "/path/to/key.pem"
# ca   = "/path/to/ca.pem"

[nats.jetstream]                  # JetStream-specific options
# stream = "archie-tasks"        # stream name (default: auto-created)
# bucket = "archie-state"        # KV bucket for task state (optional)
# replicas = 3                   # stream replication factor
# max_deliver = 10               # max delivery attempts for a message
# ack_wait = "5m"                # ack wait timeout
```

In Go, this would map to a struct like:

```go
type NATS struct {
    URL         string      `toml:"url"`
    Token       string      `toml:"token"`
    User        string      `toml:"user"`
    PasswordEnv string      `toml:"password_env"`
    TLS         *NATSTLS    `toml:"tls,omitempty"`
    JetStream   *JetStream  `toml:"jetstream,omitempty"`
}

type NATSTLS struct {
    Cert string `toml:"cert"`
    Key  string `toml:"key"`
    CA   string `toml:"ca"`
}

type JetStream struct {
    Stream     string   `toml:"stream"`
    Bucket     string   `toml:"bucket"`
    Replicas   int      `toml:"replicas"`
    MaxDeliver int      `toml:"max_deliver"`
    AckWait    Duration `toml:"ack_wait"`
}
```

And on `Config`:
```go
NATS *NATS `toml:"nats,omitempty"`  // nil = no NATS, single-process mode
```

Key style considerations:
- Existing config uses `password_env` pattern (see `token_env`, `api_key_env`) for secrets that come from environment variables
- The `Duration` type is already available for timer fields like `ack_wait`
- Using a pointer (`*NATS`) keeps the "nil means not configured" semantics, consistent with the daemon's fallback to single-process operation
- The `[nats.tls]` sub-table pattern mirrors how `[dispatch.labels]` is a sub-table under `[dispatch]`