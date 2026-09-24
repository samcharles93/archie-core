---
name: archie-debugging-playbook
description: Project-specific symptom-to-evidence triage for archie-core. Load this skill when Archied hangs, parks a task, loses or duplicates work, cannot reach NATS or an agent container, rejects configuration or secrets, shows stale Telegram or dashboard state, loses streamed text, fails MCP startup, reports task-state surprises, degrades an optional feature, or when Go tests disagree between a sandbox, editor, CI, and the host. Use it to separate environment failures from code regressions and to choose the next exact diagnostic.
---

# Debug Archie from evidence

**current** = 2026-09-18 verified (HEAD `2e1e1549`); **fixed history** =
regression patterns; **open** = verified gap.

**Composition root**: `cmd/archied/main.go` → `internal/app/archied.Run()`;
wiring lives in `internal/app/archied/bootstrap.go`. **Core NATS
request/reply**: direct subject + reply inbox. **JetStream**: persisted stream
`ARCHIE_TASKS`. **State Store**: gRPC (`internal/infrastructure/staterpc`), not
NATS.
**Environment failure**: test/process cannot use host resource (listener, tmp,
Docker). **Code regression**: behavior fails where prerequisites available.

## Run the first-ten-minutes protocol

Do not begin with `task check` (mutates via `gofumpt -w .`/`go fix ./...`).
Compile, then run:

```bash
GIT_CONFIG_GLOBAL=/dev/null go test ./PATH/TO/PACKAGE -run '^TestName$' -count=1 -v
```

Classify: compile-only, listener failure, read-only tmp, code regression.
   - Behavioral assertion with the prerequisite available: code regression.

Use this symptom index:

| Symptom | First boundary | Go to |
|---|---|---|
| `Run() = "\n"` in `TestRunWrapsExternalCommand` | Skill-script test command shape | Fixed history — passes since 2026-09-16 |
| Embedded NATS panics before a test assertion | Listener permission | Test environment |
| `nats: no responders available for request` | Core NATS subscription | NATS request/reply |
| Agent reply timeout | JetStream request, inbox, or worker | NATS request/reply |
| Queue looks empty while NATS reports an error | `Client.Fetch` batch error | NATS request/reply |
| Config test passes but daemon ignores the field | Composition/wiring | Configuration |
| Daemon has an env var but container does not | Compose or `containerEnv` | Configuration |
| Telegram reply hangs on the first token | Stream consumer | Streaming |
| MCP unit tests pass but a real server hangs | Framing mirror | MCP |
| Telegram shows old commands | Token-scoped Telegram state | Telegram |
| Transition succeeds from the wrong state | Store semantics | Database/state |
| Dashboard skips events but task state is correct | Bounded event buffers | Events |
| Search works but never becomes indexed | Missing production wiring | Optional features |
| Agent container cannot resolve `nats` | Docker network | Containers |
| `agent.mode = "subprocess"` fails at protocol startup | Worker binary mismatch | Subprocess mode |

## Separate test-environment failures from regressions

```bash
go version
go env GOTMPDIR GOCACHE GOPATH GOMODCACHE GOENV
```

Listener-dependent tests include embedded NATS, `httptest.NewServer`, and SMTP
listeners:

```bash
rg -n 'httptest\.NewServer|net\.Listen|RunRandClientPortServer|RunServer' \
  --glob '*_test.go' cmd internal
```

Do not attribute go-git commit failures to GPG without checking the
repository-local mitigation. `worktree.Manager.setIdentity` pins
`commit.gpgsign=false`. Isolate ambient global config:

```bash
GIT_CONFIG_GLOBAL=/dev/null go test ./internal/worktree -count=1
```

## Confirm the skill-script regression stays fixed

The `TestRunWrapsExternalCommand` failure (`Run() = "\n"`) was a test bug, not
a product bug: `exec.CommandContext(ctx, "sh", "-c", "echo", "wrapped")`
passes `wrapped` as shell `$0`, so only `echo` ran. It failed from commit
`308c199` and **passes as of 2026-09-16**. Re-run it to confirm the fix has not
regressed; a failure now is a new defect, not this one.

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-skillscript-gocache \
  GIT_CONFIG_GLOBAL=/dev/null go test ./internal/skillscript \
  -run '^TestRunWrapsExternalCommand$' -count=1 -v
```

## Triage NATS request/reply and JetStream

| Surface | Subject or resource | Bound/current behavior |
|---|---|---|
| Task discovery | `archie.task.>` in `ARCHIE_TASKS` | Work-queue retention; daemon durable `archie-daemon`; max deliver 3 |
| Per-stage agent request | `archie.agent.<task>.request` | Reply inbox in `X-Archie-Reply`; wall-clock budget or 30m |
| Full task handoff | `archie.taskrun.<task-id>` | Core request/reply; no-responder retry 20s every 250ms |
| Store RPC | gRPC State Store (`internal/infrastructure/staterpc`) | Client timeout 60s (`rpcTimeout` in `internal/app/agentworker/worker.go`); task-scoped grants for Update/Transition/InsertEvent only |
| Forge RPC | `archie.forge.*` | Error travels in a JSON envelope |
| Worktree RPC | `archie.worktree.prepare`, `.push` | Server default handler bound 15m |
| Discovery dedup | `Nats-Msg-Id: archie:<owner>/<repo>/<issue>` | JetStream duplicate window 2m |

```bash
docker compose ps
docker compose logs --since=10m nats
docker compose exec -T nats wget -q -O - http://localhost:8222/healthz
```

Branch on `no responders`: container startup lag if responder appears within 20s;
inspect agent's network/environment if every task exhausts window; confirm
`registerTaskRPCServers` if only RPC calls time out.

**Fixed history:** auto-detection once put task containers on default bridge;
explicit `containers.network` plus warning logs now fence that path.

Check semantics: republishing same owner/repo/issue within 2m is dedup'd; reply
inbox auto-unsubscribes after one response; `Client.Fetch` must inspect
`batch.Error()`; an empty poll reports `eventbus.ErrNoMessage` rather than
`(nil, nil)`.

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-nats-unit-gocache \
  go test ./internal/infrastructure/eventbus/nats -run 'TestPublishUniqueSuppressesDuplicateKeyOnWorkQueueStream' -count=1 -v
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-agent-unit-gocache \
  go test ./internal/agentexec -run '^TestToolSetHandler' -count=1 -v
```

## Trace configuration from source to behavior

Trace: `cmd/archied` (`internal/app/archied/bootstrap.go`) reads
`-config`/`-config-overlay` → `configuration.New(log)` + `Loader.Resolve`
decodes, `finalize` defaults/validates → secrets resolve → composition root
constructs service → Compose/subprocess/`containerEnv` propagate → consumer.

```bash
rg -n 'Loader\.(Resolve|Overlay|Dir)|configuration\.' internal/app/archied internal/infrastructure/configuration
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-config-gocache \
  go test ./internal/infrastructure/configuration \
  -run 'Test(ResolveSelectsFileFormatsAndDirectories|LoadForgeTokenEnvBackwardCompat)' -count=1 -v
```

The `config.Load`/`config.LoadDir` helpers are gone; decoding is
`Loader`-based. A YAML field decoding correctly without a composition-root read
is a wiring gap, not a parser bug.

For environment failures:
- The host supervisor supplies the daemon environment; inspect its unit or
  launch environment rather than Compose.
- `containerEnv` translates configured NATS token name into `NATS_TOKEN` and
  forwards configured provider key variables.
- `SubprocessRunner` forwards default compatibility variables, configured
  `[agent].env` names, and only the requested provider key.
- Top-level `[forge] token_env` is converted to an env `SecretRef` by
  `finalize`; explicit `[forge.token]` wins.

Do not print secret values.

## Diagnose streaming, MCP, and Telegram

### Streaming
Must drain `stream.FullStream`, collect `core.StreamPartTextDelta`, check
`FinishReason`. `TextStream` is best-effort. **Fixed history:** consuming
`TextStream` while `FullStream` unread deadlocked every reply.

### MCP
Stdio framing is one compact JSON-RPC object per line. Stale
`internal/tools/mcp/types.go` comment mentions `Content-Length` — not the
implemented contract.

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-mcp-gocache \
  go test ./internal/tools/mcp \
  -run 'Test(ReadMessage|WriteMessage|StdioTransportBadFraming)' -count=1 -v
```

Test helper and client share the same `readMessage`/`writeMessage` — can mirror
mistakes. With `npx`:

```bash
ARCHIE_TEST_DESKTOP_COMMANDER=1 env GOTMPDIR=/tmp \
  GOCACHE=/tmp/archie-mcp-integration-gocache \
  go test ./internal/tools/provider/mcp \
  -run '^TestDesktopCommanderClientCompatibility$' -count=1 -v
```

Gate: non-empty discovery with `start_process`, `interact_with_process`,
`read_process_output`.

### Telegram
Three command scopes in `internal/channels/telegram/commands.go`: default, all
private chats, all group chats. Narrower scope shadows default; state keyed by
bot token. Empty `allowed_user_ids` denies everyone.

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-telegram-gocache \
  go test ./internal/channels/telegram \
  -run 'Test(SenderAllowlistFailsClosed|IsSenderAllowed|SessionManagementCommandsArePublished)$' \
  -count=1 -v
```

**Fixed history:** publishing only default left ~60 old Hermes commands visible.

## Diagnose state, events, containers, and optional features

### Database and transitions
`internal/infrastructure/postgres.Store.Transition` owns task state changes and
transition history: it guards on `from`, returning
`storecontract.ErrStaleTransition`, and writes status plus history in one transaction.
When state is surprising: read `TaskByID` before/after caller; trace every
`Transition`, `Requeue`, `RecoverStale`, `Update` — and remember the daemon
exposes these over the gRPC State Store, not NATS.

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-state-gocache go test ./internal/infrastructure/postgres -count=1  # needs Docker
```

### Events
DB sink buffer 256; SSE client buffers 64. Drop counter not read in production.
Dashboard gap can coexist with correct task state.

### Containers, worktrees, and subprocess mode
Container-mode: clones on host, mounts `/data/worktree`, writes `.git/task.json`,
dedicated `archie.taskrun.<id>` subscription. **Open:** container RPC is
root-identity-bound; `SubprocessRunner` expects stdin JSON protocol.

### Optional feature degradation

| Feature | Current failure behavior |
|---|---|
| Daemon plugins | Warn and skip |
| Invalid MCP config | Warn and continue |
| Skill catalog | Warn, continue without tool |
| Worktree augmentation | Log error, use startup registry |
| Workspace indexing | **Open:** `indexing.NewManager` exists but has no production caller in `internal/app/archied` |
