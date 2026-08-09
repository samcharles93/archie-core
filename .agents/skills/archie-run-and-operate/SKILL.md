---
name: archie-run-and-operate
description: "Run and observe Archie safely with a host daemon and the repository's Docker Compose NATS service. Use when starting or stopping archied, running an archie-agent NATS worker, checking readiness, following logs, locating task state/worktrees/memory/container volumes, understanding inprocess/subprocess/NATS execution, inspecting the image pipeline, or planning an operational rollback without changing architecture."
---

# Run and operate Archie

Use this runbook to operate the implementation that exists on **2026-07-28**.
Treat source and tests as current; treat `docs/archive/` as history.

## Define the process shapes

| Term | Meaning here |
|---|---|
| `archied` | Resident orchestrator. Owns config, forge credentials, SQLite stores, worktrees, gateways, dashboard, optional NATS/Docker orchestration. |
| `archie-agent` | Long-running NATS worker. Consumes per-stage `archie.agent.>` requests and full-task `archie.taskrun.>` requests. Not a stdin/stdout worker. |
| Stage request | One autonomous workflow stage sent on `archie.agent.<task-id>.request`. |
| Full-task handoff | A whole workflow sent on core NATS subject `archie.taskrun.<task-id>` to a task container. |
| Work directory | `work_dir`: task clones plus memory and candidate index artifacts. |
| State path prefix | `db_path`: the configured prefix. Task/event state uses `<db_path>-tasks.sqlite`; conversation state uses `<db_path>-conversations.sqlite`. |

## Apply the operational safety boundary

Assume every daemon start can change local and external state.

- Obtain explicit authority for config, forge identity, repositories, NATS
  account, Docker host, and deployment host before starting.
- Never use production tokens for a smoke test.
- Do not call `archied -once` as a dry run. It accepts invitations, verifies
  push access, polls the forge, enqueues/labels work, reconciles PRs, processes
  tasks, and then exits.
- Do not expose the dashboard publicly. It has no authentication, and
  `POST /api/tasks/clear` deletes terminal task records.
- Do not print secret values. Record environment-variable **names**, config
  checksums, image digests, and paths instead.
- Stop with `Ctrl-C`, `SIGTERM`, or Compose. Do not use `SIGKILL` unless graceful
  shutdown has demonstrably failed.

There is no config-only or dry-run flag.

## Know the command surfaces

### `archied`

| Flag | Exact behavior |
|---|---|
| `-config <path>` | Load base TOML. Default: `$XDG_CONFIG_HOME/archie/config.toml`, or `~/.config/archie/config.toml`. |
| `-config-overlay <path>` | Decode a second TOML file into the base config before defaults and validation. |
| `-once` | Run startup plus one poll/process cycle, then exit. This changes state. |
| `-requeue <task-id>` | Requeue a parked/waiting task, then exit unless `-once` is also set. This changes state. |

### `archie-agent`

| Flag or environment | Exact behavior |
|---|---|
| `-nats-url <url>` | Select NATS; falls back to `NATS_URL`. One of them is required. |
| `-consumer <name>` | Set the durable JetStream consumer name; default `archie-agent`. |
| `NATS_TOKEN` | Optional NATS token read directly by the worker. |

Both linked binaries may also display Go's `-quickchecks` flag from
`testing/quick`; it is not an Archie operations control.

## Select the execution mode deliberately

| Config shape | What runs | Operational status on 2026-07-28 |
|---|---|---|
| `agent.mode = "inprocess"` and no `[nats]` | `archied` polls and claims from SQLite, runs workflow stages and model tools in-process. | **Production-wired candidate.** No process or OS isolation. |
| `agent.mode = "subprocess"` | `archied` starts `agent.command` per stage and expects one JSON invocation on stdin, one response on stdout. | **Open/broken with default command.** `cmd/archie-agent` is a long-running NATS worker and never calls `agentexec.ServeOne`. |
| `agent.mode = "nats"`, `[nats]`, containers disabled | `archied` runs the workflow; each autonomous stage goes to a separately started `archie-agent`. | **Implemented but operator-assembled.** Launch and supervise a worker separately. |
| `agent.mode = "nats"`, `[nats]`, `containers.enabled = true` | `archied` publishes task discovery through JetStream, prepares a worktree, spawns one agent container, sends the whole workflow on `archie.taskrun.<id>`. | **Host daemon + Compose NATS path.** `deployments/docker-nats-stack.toml` demonstrates it. |

## Run a local foreground daemon

```bash
task build
task --list
./bin/archied -h
./bin/archie-agent -h
```

Choose an explicitly approved config:

```bash
ARCHIE_CONFIG=/absolute/path/to/config.toml
test -r "$ARCHIE_CONFIG"
./bin/archied -config "$ARCHIE_CONFIG"
```

Expect JSON logs on stderr. Require: `nats connected` when `[nats]` configured;
`workflow registry built`; `memory manager started`; `web ui listening` unless
`web.listen = "off"`; gateway-start messages for configured channels;
`archied running` after startup verification. Absence of `archied running` means
startup did not complete. The daemon has no HTTP health endpoint.

Stop with `Ctrl-C`. The signal cancels active work, waits for dispatch
goroutines, stops capability and memory managers, removes Archie-labelled
containers, closes NATS, and closes the SQLite stores.

## Run a standalone NATS worker

```bash
NATS_URL=nats://127.0.0.1:4222 ./bin/archie-agent
```

Require both `nats connected` and `archie-agent ready`. Stop with `Ctrl-C`.
Do not use this to test subprocess mode.

## Run the repository Compose stack

| Repository command | Actual scope |
|---|---|
| `task docker-build` | Build only the `agent` image from `Dockerfile`. |
| `task docker-up` | Start the Compose-managed NATS service. The profiled agent entry is not started. |
| `task docker-logs` | Follow the NATS service logs. |
| `task docker-down` | Run `docker compose down`. Does not request volume deletion. |

Prefer the narrow start:

```bash
docker compose up -d nats
```

Read-only observations:

```bash
docker compose ps
docker compose logs --tail=200 nats
docker compose exec -T nats wget -q -O - http://localhost:8222/healthz
```

```bash
curl -fsS http://127.0.0.1:8484/api/summary
curl -fsS http://127.0.0.1:8484/api/tasks
```

Wait for the NATS healthcheck, then start `archied` on the host. Require the
configured host supervisor to report it running and verify the dashboard API
as the daemon readiness gate.

Stop with:

```bash
task docker-down
```

Never add `-v` during routine shutdown. The Compose `agent` service is a
build-profile image definition, not a long-running service. Actual task workers
are spawned by the host `archied` process through `/var/run/docker.sock`.

## Locate output and durable state

| Output or state | Current destination |
|---|---|
| Daemon logs | JSON on host stderr; the configured host supervisor captures them. |
| Agent logs | JSON on worker/container stderr. Task containers use `AutoRemove`. |
| Tasks and events | SQLite database at `<db_path>-tasks.sqlite`; default `~/.local/share/archie/archie.db-tasks.sqlite`. |
| Chat sessions and messages | SQLite database at `<db_path>-conversations.sqlite`. |
| Default task worktree | `<work_dir>/<owner>-<repo>/issue-<number>`; default root `~/.local/share/archie/work`. |
| Identity worktree | `<work_dir>/identity-<identity>/<owner>-<repo>/issue-<number>`. |
| Container worktree | Host worktree bind-mounted read/write at `/data/worktree`; `task.json` written under `.git/` so the agent's commit cannot sweep it onto the task branch. |
| Built-in memory | `<work_dir>/memory/MEMORY.md` and `<work_dir>/memory/USER.md`. |
| Telegram update state | `<work_dir>/telegram-update-deferrals.json` plus hashed `release-announcements-*.json`. |
| Codesearch candidates | Defaults derive as `<work_dir>/indexes` and `<work_dir>/workspace-indexes.db`; no `indexing.Manager` construction found in daemon composition root. |
| Optional repository volume | `archie-repo-<owner>-<repo>` mounted at `/data/repo`. |
| Ecosystem cache volumes | `archie-cache-go`, `archie-cache-node`, etc., mounted below `/data/cache`. No code sets tool cache env vars to those mount paths. |
| NATS data | Compose declares `nats_data:/data`; NATS command does not set a store directory. |

Compose runs NATS only; `archied` and all configured state paths remain on the
host. The host daemon talks to the local Docker socket directly and bind-mounts
each task worktree into the agent container it creates.

## Understand NATS and container observation

The `ARCHIE_TASKS` JetStream stream uses file storage and work-queue retention
for `archie.task.>` and `archie.agent.>`.

| Plane | Subjects and behavior |
|---|---|
| Discovery | `archie.task.bug`, `.feature`, `.bootstrap`, or `.default`; daemon durable consumer `archie-daemon`, 5-min ack wait, max 3 deliveries. |
| Per-stage requests | `archie.agent.<id>.request`; worker durable consumer defaults to `archie-agent`, 30-min ack wait, max 3 deliveries; response address in `X-Archie-Reply`. |
| Container task | Core NATS request/reply on `archie.taskrun.<id>`. Task container reads `/data/worktree/.git/task.json` and creates dedicated subscription; shared worker uses queue group `archie-taskrun-workers`. |
| Privileged RPC | Store, forge, and worktree operations return to `archied` over core NATS subjects in `internal/storerpc`, `internal/forgerpc`, and `internal/worktreerpc`. |

Set `containers.network` explicitly. `deployments/docker-nats-stack.toml` uses
`archie-core_default`. Auto-detection inspects the daemon container; on failure,
workers join Docker's default bridge and cannot resolve `nats`. Initial
`ErrNoResponders` is retried for 20s at 250ms intervals.

If `nats.token_env` is configured, `archied` refuses startup when that variable
is empty. It injects the resolved value as `NATS_TOKEN` into task containers.
It forwards only configured provider-key variables.

## Fence current operational limitations

Open limitations until code/tests prove otherwise:

- `subprocess` + default `archie-agent` is protocol-incompatible.
- `config.production.toml` uses legacy `token_env`; validation requires
  `forge.token` secret reference.
- Container RPC uses root forge/worktree for every identity.
- Container orphan recovery lacks daemon-instance label.
- `max_uptime` only bounds `Pool.Acquire` create/start; no lifetime timer.
- Container workflow builds `TaskContext` without event bus.
- SSE clients drop when channel full; replay at most 200 events.
- Dashboard unauthenticated, includes mutating clear endpoint.

## Understand deployment and rollback seams

The Gitea workflow builds/pushes only `archied:latest` and `archie-agent:latest`.
It injects component versions into labels/metadata but publishes no versioned
tags. It runs no quality gate before Docker builds.

Before promotion: record running image IDs/digests; config checksums (no
secrets); task counts via safe GET; establish a SQLite backup procedure.

No complete rollback command exists, no versioned image tags. Restoring a binary
does not undo forge changes, task transitions, or data-format changes.

## Treat carina facts as unverified external state

Repository guidance (2026-07-28): two instances on `carina` with distinct
`bot_user` values. No live process list, config, image digest, service manager,
or deployment was verified. Do not invent SSH, systemd, or service commands.
