---
name: archie-debugging-playbook
description: Project-specific symptom-to-evidence triage for archie-core. Load this skill when Archied hangs, parks a task, loses or duplicates work, cannot reach NATS or an agent container, rejects configuration or secrets, shows stale Telegram or dashboard state, loses streamed text, fails MCP startup, reports NellDB state surprises, degrades an optional feature, or when Go tests disagree between a sandbox, editor, CI, and the host. Use it to separate environment failures from code regressions and to choose the next exact diagnostic.
---

# Debug Archie from evidence

Use this runbook to identify the failing boundary. Stop after establishing the
smallest reproducible symptom and its owner. Do not edit code while collecting
the first evidence set.

Treat facts marked **current** as verified against the repository on
2026-07-28. Treat facts marked **fixed history** as regression patterns, not
claims that production is failing now. Treat **open** as a verified gap whose
production impact still depends on deployment state.

Define these terms once:

- **Composition root**: `cmd/archied/main.go`, where decoded configuration
  becomes live services.
- **Core NATS request/reply**: a request sent directly to a subject and answered
  through its reply inbox.
- **JetStream**: the persisted NATS task/agent stream named `ARCHIE_TASKS`.
- **Wiring**: construction and connection of an implemented package into the
  running daemon.
- **Environment failure**: a test or process cannot use a host resource such as
  a listener, temp directory, Docker socket, or credential.
- **Code regression**: the same focused behavior fails in an environment that
  provides its declared prerequisites.

## Do not use this skill for the wrong job

- For the incident timeline, rejected fixes, or reverts, load
  `archie-failure-archaeology`.
- For AST traversal, callers, duplicate paths, or feature impact, load
  `archie-codebase-discovery`.
- For repeatable inventories and measurements, load
  `archie-diagnostics-and-tooling`.
- For installing tools or recreating the machine, load `archie-build-and-env`.
- For live startup, Compose, deployment, logs, or rollback, load
  `archie-run-and-operate`.
- For acceptance evidence after a fix, load `archie-validation-and-qa`, then
  route behavior-changing work through `archie-change-control`.
- For NellDB design or persistence changes, load `archie-nelldb`.

## Run the first-ten-minutes protocol

1. Record the exact command, exit code, first causal error, working directory,
   configuration paths, execution mode, and whether the run was sandboxed.
2. Reproduce one package or one process boundary. Do not begin with `task
   check`: it writes through `gofumpt -w .` and `go fix ./...`.
3. Compile tests without running them:

   ```bash
   env GOTMPDIR=/tmp GOCACHE=/tmp/archie-debug-gocache \
     GIT_CONFIG_GLOBAL=/dev/null go test ./PATH/TO/PACKAGE -run '^$' -count=1
   ```

4. Run only the named test:

   ```bash
   env GOTMPDIR=/tmp GOCACHE=/tmp/archie-debug-gocache \
     GIT_CONFIG_GLOBAL=/dev/null go test ./PATH/TO/PACKAGE \
     -run '^TestName$' -count=1 -v
   ```

5. Classify the first failure:

   - Compile-only failure: inspect code, generated inputs, build tags, and the
     actual Go toolchain.
   - `listen ... operation not permitted` or an embedded NATS panic: rerun in an
     environment that permits loopback listeners before blaming code.
   - `mkdir /work/tmp/...: read-only file system`: keep `GOTMPDIR=/tmp`; the
     repository code has not run yet.
   - Behavioral assertion with the prerequisite available: treat it as a code
     regression.
6. Preserve the failing output. Predict the expected observation before trying
   a workaround.

Use this symptom index to select the detailed branch:

| Symptom | First boundary | Go to |
|---|---|---|
| `Run() = "\n"` in `TestRunWrapsExternalCommand` | Skill-script test command shape | Current known regression |
| Embedded NATS panics before a test assertion | Listener permission | Test environment |
| `nats: no responders available for request` | Core NATS subscription | NATS request/reply |
| Agent reply timeout | JetStream request, inbox, or worker | NATS request/reply |
| Queue looks empty while NATS reports an error | `Client.Fetch` batch error | NATS request/reply |
| Config test passes but daemon ignores the field | Composition/wiring | Configuration |
| Daemon has an env var but container does not | Compose or `containerEnv` | Configuration |
| Telegram reply hangs on the first token | Stream consumer | Streaming |
| MCP unit tests pass but a real server hangs | Framing mirror | MCP |
| Telegram shows old commands | Token-scoped Telegram state | Telegram |
| Transition succeeds from the wrong state | Store semantics | NellDB/state |
| Dashboard skips events but task state is correct | Bounded event buffers | Events |
| Search works but never becomes indexed | Missing production wiring | Optional features |
| Agent container cannot resolve `nats` | Docker network | Containers |
| `agent.mode = "subprocess"` fails at protocol startup | Worker binary mismatch | Subprocess mode |
| IDE reports an error that CLI cannot reproduce | Editor/cache state | Test environment |

## Separate test-environment failures from regressions

Inspect the active Go paths before changing code:

```bash
go version
go env GOTMPDIR GOCACHE GOPATH GOMODCACHE GOENV
```

This checkout can inherit `GOTMPDIR=/work/tmp`; that path is read-only in some
sandboxes. Setting both `GOTMPDIR=/tmp` and a task-specific `GOCACHE` is a
diagnostic isolation step, not a product fix.

Listener-dependent tests include embedded NATS, `httptest.NewServer`, and SMTP
listeners. Locate them:

```bash
rg -n 'httptest\.NewServer|net\.Listen|RunRandClientPortServer|RunServer' \
  --glob '*_test.go' cmd internal
```

If a listener is forbidden, the characteristic evidence is a panic such as
`Unable to start NATS Server in Go Routine` or
`httptest: failed to listen on a port ... operation not permitted`. A
listener-free package passing does not waive the blocked integration tests.

Do not attribute current go-git commit failures to GPG without checking the
repository-local mitigation. `worktree.Manager.setIdentity` pins
`commit.gpgsign=false`; its fixtures do the same. Isolate ambient global config:

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-worktree-gocache \
  GIT_CONFIG_GLOBAL=/dev/null go test ./internal/worktree -count=1
```

If this passes while an editor still reports compiler errors, record the CLI
result as authoritative and restart the editor language server. Do not delete
module caches merely to silence a diagnostic.

**Fixed history:** `.gotmp/` once held a workaround for tmpfs quota pressure and
was accidentally committed by a broad add. It is ignored now. Keep diagnostics
in `/tmp`, and never turn a local cache workaround into repository content.

## Confirm the current known regression

As of 2026-07-28, this focused test fails in an otherwise runnable package:

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-skillscript-gocache \
  GIT_CONFIG_GLOBAL=/dev/null go test ./internal/skillscript \
  -run '^TestRunWrapsExternalCommand$' -count=1 -v
```

Expected current failure:

```text
Run() = "\n"
```

The test constructs:

```go
exec.CommandContext(ctx, "sh", "-c", "echo", "wrapped")
```

For `sh -c`, the argument after the command string becomes shell `$0`;
therefore the command string is only `echo`, which prints a newline. This is a
test regression introduced by cleanup commit `308c199`, not proof that Yaegi
cannot run external commands. Do not weaken the expected `"wrapped\n"` value.
Route the fix and its acceptance evidence through `archie-change-control` and
`archie-validation-and-qa`.

## Triage NATS request/reply and JetStream

Keep the two transports distinct:

| Surface | Subject or resource | Bound/current behavior |
|---|---|---|
| Task discovery | `archie.task.>` in `ARCHIE_TASKS` | Work-queue retention; daemon durable `archie-daemon`; max deliver 3 |
| Legacy agent stage | `archie.agent.<task>.request` | Reply inbox in `X-Archie-Reply`; wall-clock budget or 30m |
| Full task handoff | `archie.taskrun.<task-id>` | Core request/reply; no-responder retry defaults to 20s every 250ms |
| Store RPC | `archie.store.update`, `.transition` | Client timeout set to 60s by `cmd/archie-agent/taskrun.go` |
| Forge RPC | `archie.forge.*` | Error travels in a JSON envelope |
| Worktree RPC | `archie.worktree.prepare`, `.push` | Server default handler bound is 15m |
| Discovery deduplication | `Nats-Msg-Id: archie:<owner>/<repo>/<issue>` | JetStream duplicate window is 2m |

For any NATS failure, collect read-only deployment evidence:

```bash
docker compose ps
docker compose logs --since=10m archied nats
docker ps --filter label=archie-daemon=true \
  --format '{{.ID}}\t{{.Names}}\t{{.Networks}}\t{{.Status}}'
docker compose exec -T nats wget -q -O - http://localhost:8222/healthz
```

No stable container count exists; compare the task ID, agent container, and
subscription timing rather than demanding a fixed number.

Branch on `no responders`:

1. If a responder appears within 20 seconds, classify it as container startup
   lag. `Daemon.requestTaskRun` deliberately retries only
   `nats.ErrNoResponders`.
2. If every task exhausts the window, inspect the spawned agent's network and
   environment. The Compose overlay currently sets
   `containers.network = "archie-core_default"` and NATS URL
   `nats://nats:4222`.
3. Look for agent logs `nats connected`, `taskrun: dedicated per-task
   subscription`, and `archie-agent ready`. Absence of all three points before
   workflow execution.
4. If only store/forge/worktree calls time out, confirm the composition root
   reached `registerTaskRPCServers`; do not debug JetStream task discovery.

**Fixed history:** network auto-detection once silently put task containers on
the default bridge, so `nats` never resolved and every request had no
responders. Explicit `containers.network` plus warning logs now fence that
path.

Check duplicate and error semantics before "fixing" them:

- Republishing the same owner/repo/issue within two minutes is intentionally
  deduplicated.
- A reply inbox auto-unsubscribes after one response. Search for two handlers
  subscribed to the same request subject before assuming response loss.
- `Client.Fetch` must inspect `batch.Error()` after the message channel closes;
  returning `(nil, nil)` would mask a JetStream failure as an empty queue.
- `agentexec.runStages` returns a response envelope and a nil Go error for a
  stage failure. Returning both the envelope error and a Go error aborts the
  handler before it can publish the response.

Run listener-free regression coverage for the last two rules:

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-nats-unit-gocache \
  go test ./internal/nats \
  -run '^TestFetchPropagatesBatchError' -count=1 -v
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-agent-unit-gocache \
  go test ./internal/agentexec \
  -run '^TestHandleMessage' -count=1 -v
```

## Trace configuration from source to behavior

Never stop at "the field decoded." Trace this sequence:

1. `cmd/archied` reads `-config` and optional `-config-overlay`.
2. `config.LoadOverlay` TOML-decodes and `finalize` defaults/validates.
3. Secret references resolve or named environment variables are read.
4. The composition root constructs a service from the field.
5. Compose, subprocess allowlists, or `containerEnv` propagate needed values.
6. A focused behavior test observes the value at its consumer.

Use these source checks:

```bash
rg -n 'config\.LoadOverlay|config\.LoadDir' cmd/archied internal/config
rg -n 'cfg\.[A-Za-z0-9_.]+' cmd/archied/main.go
rg -n 'containerEnv|WorkerEnvironment|environment:' \
  cmd internal docker-compose.yml
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-config-gocache \
  go test ./internal/config \
  -run 'Test(LoadOverlay|LoadForgeToken|DockerConfig)' -count=1 -v
```

Expected current observation: production `cmd/archied` calls
`config.LoadOverlay`; feature-file `config.LoadDir` is covered by tests but has
no `config.LoadDir` call in the composition root. A YAML feature field can
therefore decode correctly without affecting the daemon. Classify that as a
wiring gap, not a parser bug.

For environment failures, distinguish:

- Compose passes only the names listed under `archied.environment`.
- `containerEnv` translates the configured NATS token name into `NATS_TOKEN`
  for `archie-agent` and forwards configured provider key variables.
- `SubprocessRunner` forwards default compatibility variables, configured
  `[agent].env` names, and only the requested provider key.
- The legacy top-level `[forge] token_env` is converted to an env `SecretRef`
  by `finalize`; explicit `[forge.token]` wins.

Do not print secret values. Record only the variable name and
set/unset/propagated status.

## Diagnose streaming, MCP, and Telegram

### Streaming

If a Telegram draft shows an empty cursor, typing never clears, and the turn
never completes, inspect the consumer in `cmd/archied/main.go`. Current code
must drain `stream.FullStream`, collect only `core.StreamPartTextDelta`, then
check `FinishReason`. `TextStream` is a best-effort view and cannot reconstruct
the authoritative response.

**Fixed history:** consuming `TextStream` while leaving synchronous
`FullStream` unread deadlocked every streamed reply. Do not add a second
consumer or return before `FullStream` closes.

### MCP

Current stdio framing is one compact JSON-RPC object per line. Despite a stale
package comment in `internal/tools/mcp/types.go`, `Content-Length` framing is
not the implemented contract. Verify unit behavior:

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-mcp-gocache \
  go test ./internal/tools/mcp \
  -run 'Test(ReadMessage|WriteMessage|StdioTransportBadFraming)' \
  -count=1 -v
```

Do not accept this alone as protocol compatibility: the helper server in
`transport_test.go` calls the same `readMessage` and `writeMessage` functions,
so client and fake can mirror the same mistake. With network access and `npx`
explicitly allowed, run the real-server test:

```bash
ARCHIE_TEST_DESKTOP_COMMANDER=1 \
  env GOTMPDIR=/tmp GOCACHE=/tmp/archie-mcp-integration-gocache \
  go test ./internal/tools/provider/mcp \
  -run '^TestDesktopCommanderClientCompatibility$' -count=1 -v
```

No stable tool count is asserted. The gate is non-empty discovery containing
`start_process`, `interact_with_process`, and `read_process_output`.

### Telegram

If commands work when typed but the menu is stale, inspect all three command
scopes in `internal/channels/telegram/commands.go`: default, all private chats,
and all group chats. Telegram stores command menus outside this repository,
keyed by bot token, and a narrower scope shadows the default.

If access is wrong, verify sender IDs, not chat IDs. Empty
`allowed_user_ids` denies everyone; message, model, provider, and update
callbacks each enforce sender authorization.

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-telegram-gocache \
  go test ./internal/channels/telegram \
  -run 'Test(SenderAllowlistFailsClosed|IsSenderAllowed|PublishedCommandsMatchExecutableCommandSurface)$' \
  -count=1 -v
```

**Fixed history:** publishing only the default menu left roughly 60 commands
from a previous bot implementation visible in narrower scopes. A successful
single `SetMyCommands` call was insufficient evidence.

## Diagnose state, events, containers, and optional features

### NellDB and transitions

Current `internal/nell.Adapter.Transition` looks up the task, writes `to`, and
does not compare `from` or persist `detail`. The legacy SQLite store also does
not guard its update with `from`, although it records a transition row. Thus
"transition returned nil" does not prove the source state matched.

When state is surprising:

1. Read `TaskByID` immediately before and after the caller.
2. Trace every `Transition`, `Requeue`, `RecoverStale`, and `Update` call.
3. Do not infer a valid state machine from the `from` argument.
4. Run both backends' focused suites:

   ```bash
   env GOTMPDIR=/tmp GOCACHE=/tmp/archie-state-gocache \
     GIT_CONFIG_GLOBAL=/dev/null go test \
     ./internal/nell ./internal/store -count=1
   ```

Route any semantic change to `archie-nelldb`, architecture planning, and change
control; it is not a local error-message fix.

### Events

The in-process bus drops when a subscriber buffer is full. The composition
root's database sink buffer is 256; SSE client buffers are 64. The sink's drop
counter is not read in production, and per-client drops are not counted.
Therefore a dashboard gap can coexist with correct task state.

```bash
rg -n 'Subscribe\(256\)|make\(chan events\.Event, 64\)|Dropped\(\)' \
  cmd/archied/main.go internal/events internal/webui
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-events-gocache \
  go test ./internal/events -count=1
```

No stable production drop count exists. Treat missing events as an
observability defect only after comparing the persisted task document and
event log.

### Containers, worktrees, and subprocess mode

- A container-mode task clones on the host, mounts it at `/data/worktree`,
  writes `task.json`, then uses a dedicated `archie.taskrun.<id>`
  subscription when that file parses.
- The worktree manager uses go-git, pins signing off, stores its prepared
  sentinel under `.git`, and performs an independent full clone per task.
- **Open:** container RPC registration is root-identity-bound; the comment in
  `Daemon.process` says non-root identity container calls can use the wrong
  forge/worktree clients.
- **Open:** the default `archie-agent` binary is now a long-running NATS worker,
  while `SubprocessRunner` expects one JSON invocation on stdin and one JSON
  response on stdout. `agent.mode="subprocess"` remains accepted by config, so
  verify the configured command implements the old protocol before using it.

Confirm the binary shape without starting NATS:

```bash
env -u NATS_URL -u NATS_TOKEN GOTMPDIR=/tmp \
  GOCACHE=/tmp/archie-agent-shape-gocache \
  go run ./cmd/archie-agent </dev/null
```

Expected current observation: exit 1 with
`-nats-url or NATS_URL is required`, not a decoded stdin invocation. Label a
deployment using another compatible command separately.

### Optional feature degradation

Configuration is not capability evidence. Check startup logs and production
construction:

| Feature | Current failure behavior |
|---|---|
| Daemon plugin adaptation/registration | Warn and skip that capability |
| Invalid MCP server config | Warn `mcp tool provider skipped` and continue |
| Skill catalog/activation registration | Warn and continue without the tool |
| Worktree workflow augmentation | Log error, emit `registry_augment_failed`, use startup registry |
| Workspace indexing | **Open:** config defaults and helper exist, but no production `indexing.NewManager` call |

Verify the indexing wiring gap:

```bash
rg -n 'indexing\.NewManager|NewManager\(.*Index' \
  cmd internal --glob '*.go' --glob '!*_test.go'
```

Expected current result: no production call. Do not diagnose slow unindexed
search by tuning index paths until this wiring changes.

## Close the diagnostic

Record:

- the smallest failing command and its prerequisite;
- the first causal error, not the final wrapper;
- the boundary owner and current/fixed-history/open classification;
- the expected observation and actual observation;
- the source symbol and focused test that protect the behavior;
- whether the result reproduces outside the sandbox;
- the next sibling skill and change-control class.

Do not call a diagnosis complete because a workaround made the symptom vanish.
Require a falsifiable cause: removing or restoring the suspected condition must
toggle the failure.

## Provenance and maintenance

Repository state and volatile facts were re-verified on 2026-07-28. Re-run
these one-line checks after changes:

```bash
rg -n 'defaultTaskRunReadyTimeout|defaultTaskRunRetryBackoff' internal/daemon/daemon.go
rg -n 'dedupWindow|pollTimeout|AckWait|MaxDeliver' internal/nats/client.go cmd/archie-agent/main.go
rg -n 'FullStream|TextStream|FinishReason' cmd/archied/main.go
rg -n 'Content-Length|newline-delimited' internal/tools/mcp
rg -n 'commandScopes|AllowedUserIDs|isSenderAllowed' internal/channels/telegram
rg -n 'func \(a \*Adapter\) Transition|func \(s \*Store\) Transition' internal/nell/adapter.go internal/store/store.go
rg -n 'Subscribe\(256\)|Dropped\(\)' cmd/archied/main.go internal/events
rg -n 'config\.LoadOverlay|config\.LoadDir|indexing\.NewManager' cmd internal --glob '*.go'
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-skillscript-gocache go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v
```
