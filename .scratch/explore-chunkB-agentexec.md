# agentexec

Now I have all the information needed. Here is the comprehensive report.

---

## 1. All Fields of Invocation, Request, Result, Response

All types are defined in `/work/apps/archie-core/internal/agentexec/protocol.go`.

**Invocation** (lines 139-144):
```go
Version   int                  `json:"version"`
Workspace string               `json:"workspace"`
Request   Request              `json:"request"`
Providers map[string]Provider  `json:"providers"`
```

**Request** (lines 59-74):
```go
Version      int           `json:"version"`
TaskID       int64         `json:"task_id"`
Attempt      int           `json:"attempt"`
Stage        string        `json:"stage"`
Model        string        `json:"model"`
Mission      string        `json:"mission"`
ExtraRules   string        `json:"extra_rules,omitempty"`
ReadOnly     bool          `json:"read_only,omitempty"`
Budget       Budget        `json:"budget"`
Gate         Gate          `json:"gate"`
Preflight    []Command     `json:"preflight,omitempty"`
Protection   Protection    `json:"protection"`
Notes        string        `json:"notes,omitempty"`
CaptureTools []CaptureTool `json:"capture_tools,omitempty"`
```

**Result** (lines 97-111):
```go
Version       int                          `json:"version"`
TaskID        int64                        `json:"task_id"`
Attempt       int                          `json:"attempt"`
Stage         string                       `json:"stage"`
Status        string                       `json:"status"`
StopReason    string                       `json:"stop_reason,omitempty"`
Changes       []string                     `json:"changes,omitempty"`
Iterations    int                          `json:"iterations,omitempty"`
TokensUsed    int                          `json:"tokens_used,omitempty"`
Summary       string                       `json:"summary,omitempty"`
Detail        string                       `json:"detail,omitempty"`
AppendedNotes []string                     `json:"appended_notes,omitempty"`
Captures      map[string][]json.RawMessage `json:"captures,omitempty"`
```

**Response** (lines 168-172):
```go
Version int    `json:"version"`
Result  Result `json:"result"`
Error   string `json:"error,omitempty"`
```

Supporting types referenced above:

- **Budget** (lines 31-35): `MaxSteps int`, `MaxTokens int`, `WallClock time.Duration`
- **Gate** (lines 25-28): `Commands []Command`, `MaxConsecutiveFailures int`
- **Command** (lines 18-22): `Name string`, `Argv []string`, `ExpectFailure bool`
- **Protection** (lines 38-41): `Suffixes []string`, `Globs []string`
- **CaptureTool** (lines 45-53): `Name string`, `Description string`, `Parameters json.RawMessage`, `RequiredFields []string`, `NonEmptyStrings []string`, `BooleanFields []string`, `MaxCalls int`
- **Provider** (lines 115-119): `Class string`, `APIKeyEnv string`, `BaseURL string`

---

## 2. ServeOne Step by Step

Defined in `/work/apps/archie-core/internal/agentexec/worker.go`.

**`ServeOne` (lines 16-20)** is the public entry point. It delegates to `serveOne` with a factory closure that, given an `Invocation`, returns a runner:

```go
func ServeOne(ctx context.Context, in io.Reader, out io.Writer, log *slog.Logger) error {
    return serveOne(ctx, in, out, func(invocation Invocation) Runner {
        return NewInProcessRunner(NewRuntime(invocation.Providers), log)
    })
}
```

**`serveOne` (lines 22-43)** does:

1. **Decode invocation** (line 24): Calls `decodeOne(in, &invocation)` which reads up to `maxProtocolBytes+1` bytes (8 MB + 1) from `in`. Rejects if the value exceeds 8 MB. JSON-decodes into the value (here, `Invocation`). Also checks there is exactly one JSON value (rejects trailing JSON values, line 58-61).

2. **Validate invocation** (line 27): Calls `invocation.Validate()` which checks:
   - `Version == ProtocolVersion` (must be 1)
   - `Workspace` is non-empty
   - `Providers` map is non-empty, and every entry's `Class` is non-empty and `BaseURL` is parseable without userinfo/query/fragment
   - The inner `Request.Validate()`: `Version` must be 1, `TaskID` > 0, `Attempt` > 0, `Stage` non-empty, `Model` non-empty

3. **Create runner** (line 30): Calls `newRunner(invocation)` -- in ServeOne's case, `NewInProcessRunner(NewRuntime(invocation.Providers), log)`.

4. **Run agent** (line 34): `runner.Run(ctx, invocation.Workspace, invocation.Request)` returns `result, runErr`.

5. **Build response** (lines 35-38): Constructs `Response{Version: ProtocolVersion, Result: result}`. If `runErr != nil`, sets `response.Error = runErr.Error()`.

6. **Encode response to stdout** (lines 39-41): `json.NewEncoder(out).Encode(response)`. Returns error if encoding fails.

7. **Return nil** on success.

Key design detail: The `serveOne` generic function separates the wire protocol (stdin/stdout JSON framing) from the runner creation strategy. The factory `func(Invocation) Runner` allows different strategies (in-process vs. subprocess vs. future NATS). Currently `ServeOne` hardcodes the in-process factory, but a future NATS-based archie-agent could call `serveOne` with a different factory or skip it entirely.

---

## 3. SubprocessRunner.Run Step by Step

Defined in `/work/apps/archie-core/internal/agentexec/subprocess.go`.

The `SubprocessRunner` struct (lines 17-27):
```go
type SubprocessRunner struct {
    Command       string
    Args          []string
    Environ       []string          // daemon's os.Environ()
    AdditionalEnv []string          // operator-approved env var names
    Diagnostics   io.Writer         // typically os.Stderr
    Providers     map[string]Provider
}
```

**`Run` (lines 29-94)**:

1. **Extract provider name from model** (lines 30-32): Splits `req.Model` at the first "/". E.g. `"anthropic/claude-sonnet-4"` yields providerName=`"anthropic"`. If no "/" or empty, returns error.

2. **Look up provider** (lines 34-37): Indexes `r.Providers[providerName]`. Missing returns error.

3. **Build and validate invocation** (lines 38-42): Creates `Invocation{Version, Workspace, Request, Providers}` with just that one provider entry. Validates.

4. **Check command** (lines 43-45): `strings.TrimSpace(r.Command)` must be non-empty.

5. **Marshal invocation** (lines 46-50): `json.Marshal(invocation)` + appends newline.

6. **Create OS command** (line 52): `exec.CommandContext(ctx, r.Command, r.Args...)`.

7. **Configure process cancellation** (line 53): `configureProcessCancellation(cmd)` -- on Unix this sets `Setpgid: true` and a `Cancel` function that does `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` (see `/work/apps/archie-core/internal/agentexec/subprocess_process_unix.go` lines 15-27). This ensures the worker process AND all its grandchildren die on context cancellation.

8. **Set stdin** (line 54): `cmd.Stdin = bytes.NewReader(payload)`.

9. **Set environment** (line 55): `WorkerEnvironment(r.Environ, r.AdditionalEnv, providers)` which (lines 155-162):
   - Starts with `DefaultEnvironmentNames()`: `PATH, HOME, TMPDIR, TMP, TEMP, LANG, LC_ALL, SSL_CERT_FILE, SSL_CERT_DIR`
   - Appends `r.AdditionalEnv`
   - Appends each provider's `APIKeyEnv`
   - Calls `SelectEnvironment` which copies only those named variables from `r.Environ`, preserving order.

10. **Capture stdout and stderr** (lines 56-62):
    - Stdout: `cappedBuffer{limit: maxProtocolBytes}` (8 MB). If `r.Diagnostics != nil`, stderr goes to `io.MultiWriter(r.Diagnostics, stderr)` (tee to both the diagnostics writer and the capped buffer). Otherwise just the capped buffer (64 KB limit via `maxDiagnosticBytes`).

11. **Run the process** (lines 63-71): `cmd.Run()`. On error:
    - If `context.Cause(ctx)` is set (cancellation), returns the cause wrapped with any stderr diagnostic.
    - Otherwise returns the exec error with stderr output.

12. **Check stdout truncation** (lines 72-74): If `stdout.truncated` is true, return error.

13. **Decode response** (lines 75-78): `decodeOne(bytes.NewReader(stdout.Bytes()), &response)` -- same framing as the reader side.

14. **Validate response version** (lines 79-81): Must match `ProtocolVersion` (1).

15. **Validate result against request** (lines 82-91):
    - If `response.Result.Version != 0`, calls `response.Result.ValidateFor(req)` -- checks Version==1, TaskID/Attempt/Stage identity match, Status non-empty.
    - If `response.Error != ""`, returns result with `errors.New(response.Error)`.
    - Validates one more time at line 90 for the non-error case.

16. **Return** (line 93): `response.Result, nil`.

---

## 4. NewRuntime and InProcessRunner

**`NewRuntime`** in `/work/apps/archie-core/internal/agentexec/runtime.go` (lines 7-23):
- Takes `providers map[string]Provider` (agentexec.Provider type).
- Returns `*runtime.Runtime` (from `github.com/samcharles93/ai-sdk/runtime`).
- If providers is empty, returns `nil`.
- Calls `runtime.RegisterBuiltinClasses()` (global, side-effecting).
- For each entry: creates `runtime.ProviderConfig{ID: name, Class: provider.Class, BaseURL: provider.BaseURL}`.
- If `provider.APIKeyEnv == ""`, sets `Auth = AuthConfig{Type: AuthTypeNone}`, otherwise `Auth = AuthConfig{APIKeyEnv: provider.APIKeyEnv}`.
- Calls `runtime.NewRuntime(runtime.Config{Providers: catalog})` and returns it.

**`InProcessRunner`** in `/work/apps/archie-core/internal/agentexec/inprocess.go` (lines 29-33):
```go
type InProcessRunner struct {
    runtime *runtime.Runtime
    log     *slog.Logger
    run     loopFunc   // func(context.Context, agentloop.Config) (agentloop.Result, error)
}
```

**`NewInProcessRunner`** (lines 35-37): Stores runtime, log, and sets `run = agentloop.Run` (the exported function from the `agentloop` package).

**`InProcessRunner.Run`** (lines 39-86):
1. Validates `req` via `req.Validate()`.
2. Checks `r.runtime != nil` and `r.run != nil`.
3. Creates `memoryNotes` struct (lines 219-228 of inprocess.go) -- implements a simple `Load`/`Append` interface. Initialized with `req.Notes`, collects `appended` entries.
4. Creates `captures` map: `map[string][]json.RawMessage`.
5. Calls `r.run(ctx, agentloop.Config{...})` with all fields mapped from `req`:
   - `Runtime: r.runtime`
   - `ModelRef: req.Model`
   - `WorkDir: workspace`
   - `Mission: req.Mission`, `ExtraRules: req.ExtraRules`
   - `Notes: notes` (the memoryNotes)
   - `Gate: toAgentGate(req.Gate)` (converts agentexec.Gate -> agentloop.GateConfig)
   - `Preflight: toAgentCommands(req.Preflight)`
   - `Budget: agentloop.Budget(req.Budget)` (direct cast)
   - `ReadOnly: req.ReadOnly`
   - `ProtectPaths: protectionMatcher(...)` (closure that checks suffixes/globs)
   - `Extra: mergeToolSets(captureToolSet(req.CaptureTools, captures), scriptToolSet(workspace))`
   - `Logger: r.logger(req)` (structured with task/attempt/stage/model)
6. Builds `Result` from the returned `agentloop.Result` fields.
7. Checks `context.Cause(ctx)` for cancellation -- if cancelled, returns result with cancellation cause as error.
8. Returns `result, err`.

---

## 5. Provider Types

There are **two separate `Provider` structs** with identical fields but different packages and tags:

**`agentexec.Provider`** (`/work/apps/archie-core/internal/agentexec/protocol.go`, lines 115-119):
```go
type Provider struct {
    Class     string `json:"class"`
    APIKeyEnv string `json:"api_key_env,omitempty"`
    BaseURL   string `json:"base_url,omitempty"`
}
```
Used for: JSON serialization to the subprocess worker, and as the type consumed by `NewRuntime` and `SubprocessRunner.Providers`.

**`config.Provider`** (`/work/apps/archie-core/internal/config/config.go`, lines 125-129):
```go
type Provider struct {
    Class     string `toml:"class"`
    APIKeyEnv string `toml:"api_key_env"`
    BaseURL   string `toml:"base_url"`
}
```
Used for: TOML deserialization from the config file.

**Bridge** (`/work/apps/archie-core/cmd/archied/main.go`, lines 182-188):
```go
func executionProviders(cfg config.Config) map[string]agentexec.Provider {
    providers := make(map[string]agentexec.Provider, len(cfg.Providers))
    for name, p := range cfg.Providers {
        providers[name] = agentexec.Provider{Class: p.Class, APIKeyEnv: p.APIKeyEnv, BaseURL: p.BaseURL}
    }
    return providers
}
```
The daemon composition root converts config.Provider to agentexec.Provider by field-wise copy and then passes them to both `NewRuntime` and `SubprocessRunner`.

---

## 6. How drainNATS Interacts with SubprocessRunner

**Current state: drainNATS only affects task distribution, not agent execution.**

The flow:

1. **Daemon.Cycle** (daemon.go lines 94-103): If `d.Nats != nil`, calls `d.drainNATS(ctx)`, else `d.drainSQLite(ctx)`.

2. **drainNATS** (lines 126-151): Calls `d.Nats.Fetch(ctx)` to get a JetStream message. If found, calls `processNATSTask`. Falls back to `drainSQLite` logic (SQLite `ClaimNext` then `d.process`) if no NATS message.

3. **processNATSTask** (lines 219-253): Decodes `TaskMessage`, enqueues to SQLite, claims the task, then calls `d.process(ctx, task)` -- the **same** `process` function used by the SQLite path.

4. **d.process** (lines 472-493): Routes the task to the correct workflow, then calls:
   ```go
   workflow.Run(ctx, wf, &workflow.TaskContext{
       ...
       Agent: d.Agent,   // line 488 -- the agentexec.Runner interface
       ...
   })
   ```

5. **workflow.Run** (workflow.go line 145): Iterates stages. For LLM-driven stages, calls `AgentStage.Stage()` (agent.go line 110):
   ```go
   res, err := tc.Agent.Run(ctx, tc.Dir, req)
   ```
   This `Agent` field is the `agentexec.Runner` interface. It was wired in `cmd/archied/main.go` (lines 124-138):
   - If `cfg.Agent.Mode == "subprocess"`: creates `&agentexec.SubprocessRunner{...}` with `Command`, `Environ`, `AdditionalEnv`, `Diagnostics`, `Providers`.
   - If `cfg.Agent.Mode == "inprocess"`: creates `agentexec.NewInProcessRunner(llm, log)`.

6. **SubprocessRunner.Run** spawns a fresh `archie-agent` binary per stage, sends JSON on stdin, reads JSON from stdout.

7. **The daemon's NATS `processNATSTask` does NOT interact with SubprocessRunner directly** -- it only fetches the task message from NATS, enqueues it to SQLite, and claims it. The actual agent execution happens through `d.Agent.Run` which remains either in-process or subprocess regardless of the NATS path.

---

## Design Implications for NATSRunner and NATS-based archie-agent

**Runner interface** (`/work/apps/archie-core/internal/agentexec/inprocess.go`, line 20-22):
```go
type Runner interface {
    Run(ctx context.Context, workspace string, req Request) (Result, error)
}
```

A new `NATSRunner` would need to implement this same interface. It would:

1. Accept a NATS connection, a subject prefix, and the provider map (or the whole Invocation fields).
2. In `Run(ctx, workspace, req)`:
   - Build an `Invocation` (same as `SubprocessRunner.Run` does on lines 38-42).
   - Publish it to a NATS subject (e.g. `archie.stage.request.<taskID>`).
   - Include a reply subject in the message headers or as part of the payload.
   - Wait for the response on the reply subject (with the context's deadline).
   - Decode the `Response`, validate, return `Result`.
3. No need for stdin/stdout marshalling or process management.

**New archie-agent** (`cmd/archie-agent/main.go`): Instead of `ServeOne(ctx, os.Stdin, os.Stdout, log)`, it would:
1. Connect to NATS.
2. Subscribe to a subject like `archie.stage.request.>`.
3. For each message: decode `Invocation` from the message body, call `serveOne`-style logic (validate, build runner, run, build response), publish `Response` back to the reply subject, ack.
4. Could be a long-running daemon with a pool of subscribers.

**Key integration point**: The daemon composition root (`cmd/archied/main.go` lines 124-138) would gain a third branch:
```go
case "nats":
    agentRunner = &agentexec.NATSRunner{
        Conn:      natsClient.Conn(),  // or a new NATS connection
        Subject:   "archie.stage.request",
        Providers: providers,
    }
```

And in `config.go` the `Agent.Mode` validation (lines 298-305) would accept `"nats"` in addition to `"inprocess"` and `"subprocess"`.

**The `serveOne` generic function** in `worker.go` (lines 22-43) is already well-factored: it separates wire protocol from runner creation. A NATS-based archie-agent could reuse `serveOne` by providing a factory that creates an in-process runner, or it could have its own message handler that calls the same `Validate` -> `Runner.Run` -> `Response` logic.