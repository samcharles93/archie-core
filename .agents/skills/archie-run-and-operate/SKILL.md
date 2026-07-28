---
name: archie-run-and-operate
description: "Run and observe Archie safely from a local binary or the repository Docker Compose stack. Use when starting or stopping archied, running an archie-agent NATS worker, checking readiness, following logs, locating task state/worktrees/memory/container volumes, understanding inprocess/subprocess/NATS execution, inspecting the deployment watcher or image pipeline, or planning an operational rollback without changing architecture."
---

# Run and operate Archie

Use this runbook to operate the implementation that exists on **2026-07-28**.
Treat source and tests as current; treat `docs/archive/` as history, not an
operational contract.

## Route work to the right skill

Do **not** use this skill to:

- install Go, Task, Docker, or project dependencies; use
  `archie-build-and-env`;
- choose, add, or migrate flags and TOML fields; use
  `archie-config-and-flags`;
- diagnose a failure beyond the first observations below; use
  `archie-debugging-playbook`;
- inspect or repair NellDB data; use `archie-nelldb`;
- decide whether a runtime or deployment change is acceptable; use
  `archie-change-control` and `archie-validation-and-qa`;
- redesign process boundaries or deployment topology; use
  `archie-architecture-planning-campaign` and
  `archie-architecture-contract`.

## Define the process shapes

| Term | Meaning here |
|---|---|
| `archied` | Resident orchestrator. It owns config, forge credentials, the NellDB store, worktrees, gateways, the dashboard, and optional NATS/Docker orchestration. |
| `archie-agent` | Current long-running NATS worker. It consumes legacy per-stage requests and full-task `archie.taskrun.>` requests. It is **not** a stdin/stdout worker. |
| Stage request | One autonomous workflow stage sent on `archie.agent.<task-id>.request`. |
| Full-task handoff | A whole workflow sent on core NATS subject `archie.taskrun.<task-id>` to a task container. |
| Work directory | `work_dir`: task clones plus memory and candidate index artifacts. |
| State database | `db_path`: the NellDB append-log file holding tasks, events, and Nell-backed chat sessions/messages. It is not SQLite despite the `.db` examples and stale comments. |

## Apply the operational safety boundary

Assume every daemon start can change local and external state.

- Obtain explicit authority for the config, forge identity, repositories, NATS
  account, Docker host, and deployment host before starting anything.
- Never use production tokens for a smoke test.
- Do not call `archied -once` as a dry run. It accepts invitations, verifies
  push access, polls the forge, enqueues/labels work, reconciles PRs, processes
  tasks, and then exits.
- Do not call `archied -requeue` while observing. It mutates durable task state.
- Do not start a standalone `archie-agent` against an unfamiliar NATS URL. It
  immediately joins the worker pool and may execute queued work.
- Do not expose the dashboard publicly. It has no authentication, and
  `POST /api/tasks/clear` deletes terminal task records even though the package
  describes the dashboard as read-only.
- Do not print secret values. Record environment-variable **names**, config
  checksums, image digests, and paths instead.
- Stop with `Ctrl-C`, `SIGTERM`, or Compose. Do not use `SIGKILL` unless graceful
  shutdown has demonstrably failed.

There is no config-only or dry-run flag. Validate a candidate config through
`archie-config-and-flags` and tests before allowing the daemon to open it.

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

Both linked binaries may also display Go's dependency-registered
`-quickchecks` flag. It belongs to `testing/quick`; it is not an Archie
operations control.

## Select the execution mode deliberately

| Config shape | What runs | Operational status on 2026-07-28 |
|---|---|---|
| `agent.mode = "inprocess"` and no `[nats]` | `archied` polls and claims from NellDB, then runs workflow stages and model tools in its own process. | **Production-wired candidate.** `config.production.toml` selects it, but live production was not inspected. No process or OS isolation. |
| `agent.mode = "subprocess"` | `archied` starts `agent.command` per stage and expects one JSON invocation on stdin and one response on stdout. | **Open/broken with the default command.** `cmd/archie-agent` is a long-running NATS worker and never calls `agentexec.ServeOne`. No compatible repository entrypoint was found. |
| `agent.mode = "nats"`, `[nats]`, containers disabled | `archied` still runs the workflow, but each autonomous stage goes to a separately started `archie-agent`. | **Implemented but operator-assembled.** The Compose `agent` service is not a running worker; launch and supervise a worker separately. |
| `agent.mode = "nats"`, `[nats]`, `containers.enabled = true` | `archied` publishes task discovery through JetStream, prepares a worktree, spawns one agent container, and sends the whole workflow on `archie.taskrun.<id>`. | **Repository Compose path.** `config.docker.toml` wires it. Live production status is unknown, and the limitations below remain. |

Config validation requires containers to use NATS mode, a non-empty NATS URL,
and a container image.

## Run a local foreground daemon

First build through `archie-build-and-env`. The non-mutating build commands in
that skill write binaries to `/tmp/`; the commands below expect `task build` to
have placed them in `bin/`. Either run `task build` first, or set explicit
variables:

```bash
# Either use task build (authorized writable worktree only):
task build
# …or point at the /tmp binaries from the build skill's cold-start sequence:
# ARCHIE_DAEMON_BIN=/tmp/archie-core-archied
# ARCHIE_AGENT_BIN=/tmp/archie-core-agent
```

Then perform only read-only orientation:

```bash
task --list
./bin/archied -h
./bin/archie-agent -h
```

Choose an explicitly approved config; do not substitute
`config.example.toml` blindly because its repository and forge values are
examples:

```bash
ARCHIE_CONFIG=/absolute/path/to/config.toml
test -r "$ARCHIE_CONFIG"
```

Start in the foreground only after the safety boundary is satisfied:

```bash
./bin/archied -config "$ARCHIE_CONFIG"
```

Apply an approved deployment overlay with:

```bash
ARCHIE_OVERLAY=/absolute/path/to/config.overlay.toml
test -r "$ARCHIE_OVERLAY"
./bin/archied -config "$ARCHIE_CONFIG" -config-overlay "$ARCHIE_OVERLAY"
```

Expect JSON logs on stderr. Require the following sequence as applicable:

1. `nats connected` when `[nats]` is configured.
2. `workflow registry built`.
3. `memory manager started`.
4. `web ui listening` unless `web.listen = "off"`.
5. gateway-start messages for configured Telegram, email, or webhook channels.
6. `archied running` after startup verification and before the resident loop.

Absence of `archied running` means startup did not complete. The daemon has no
HTTP health endpoint and no container healthcheck; a running process alone is
not readiness evidence.

Stop the foreground process with `Ctrl-C`. The signal cancels active work,
waits for dispatch goroutines, stops capability and memory managers, removes
Archie-labelled containers, closes NATS, and closes the NellDB log. Allow the
bounded cleanup calls to finish.

## Run a standalone NATS worker

Use this only for an approved NATS-without-containers deployment:

```bash
NATS_URL=nats://127.0.0.1:4222 ./bin/archie-agent
```

Require both `nats connected` and `archie-agent ready`. Stop with `Ctrl-C`.
Assign a non-default `-consumer` only when the queue topology has been reviewed;
consumer names define durable JetStream state.

Do not use this command to test subprocess mode. The worker requires NATS and
does not speak the subprocess JSON protocol.

## Run the repository Compose stack

Read `docker-compose.yml` and the host base config before acting. Compose mounts
`${HOME}/.config/archie/config.toml` as the base and
`config.docker.toml` as the overlay. It also mounts the Docker socket and
host data directories, so this is host-authoritative execution.

| Repository command | Actual scope |
|---|---|
| `task docker-build` | Build only the `agent` image from `Dockerfile`. It does not build `archied`. |
| `task docker-up` | Run `docker compose up -d` for **all** services, including watchtower. |
| `task docker-logs` | Follow only the `archied` service logs. |
| `task docker-down` | Run `docker compose down`. It does not request volume deletion. |

Prefer the narrow start when a deployment watcher is not intended:

```bash
docker compose up -d nats archied
```

Use read-only observations:

```bash
docker compose ps
docker compose logs --tail=200 archied
docker compose logs --tail=200 nats
docker compose exec -T nats wget -q -O - http://localhost:8222/healthz
```

If a local HTTP client is installed, inspect only GET endpoints:

```bash
curl -fsS http://127.0.0.1:8484/api/summary
curl -fsS http://127.0.0.1:8484/api/tasks
```

The NATS healthcheck must become healthy before Compose starts `archied`.
Require `archied running` in logs as the daemon readiness gate. Do not invoke
`POST /api/tasks/clear`.

Stop the repository stack with:

```bash
task docker-down
```

Never add `-v` during routine shutdown; `nats_data` and any Compose-managed
volumes may contain durable state.

The Compose `agent` service is a build-only placeholder: it clears the image
entrypoint and runs `true`. Actual task workers are spawned by `archied`
through `/var/run/docker.sock`.

## Locate output and durable state

| Output or state | Current destination |
|---|---|
| Daemon logs | JSON on stderr; Docker's configured log driver captures them under Compose. The repository defines no log file or rotation policy. |
| Agent logs | JSON on worker/container stderr. Task containers use `AutoRemove`; capture logs while the container exists because the repository defines no agent log archive. |
| Tasks, events, chat sessions/messages | NellDB append log at `db_path`; default `~/.local/share/archie/archie.db`. |
| Default task worktree | `<work_dir>/<owner>-<repo>/issue-<number>`; default root `~/.local/share/archie/work`. |
| Identity worktree | `<work_dir>/identity-<identity>/<owner>-<repo>/issue-<number>`. |
| Container worktree | Host worktree bind-mounted read/write at `/data/worktree`; `task.json` is written at its root and ignored by this repository's `.gitignore`. |
| Built-in memory | `<work_dir>/memory/MEMORY.md` and `<work_dir>/memory/USER.md`. |
| Telegram update state | `<work_dir>/telegram-update-deferrals.json` plus a hashed `release-announcements-*.json`. |
| Codesearch candidates | Defaults derive as `<work_dir>/indexes` and `<work_dir>/workspace-indexes.db`; no `indexing.Manager` construction was found in the daemon composition root, so do not assume these artifacts are produced. |
| Optional repository volume | `archie-repo-<owner>-<repo>` mounted at `/data/repo`. The workflow still runs in `/data/worktree`. |
| Ecosystem cache volumes | `archie-cache-go`, `archie-cache-node`, `archie-cache-pnpm`, `archie-cache-deno`, `archie-cache-bun`, `archie-cache-pip`, or `archie-cache-cargo`, mounted below `/data/cache`. No code currently sets tool cache environment variables to those mount paths; a mount is not proof of cache use. |
| NATS data | Compose declares `nats_data:/data`, while its NATS command does not set a store directory. Verify the live server's store directory before claiming this volume preserves JetStream data. |
| Forge-visible output | Reactions, state labels, comments, pushed task branches, closed issues, and PRs. These are external mutations, not local logs. |

In Compose, the host paths are:

- `/var/lib/archie/work` for worktrees;
- `/var/lib/archie/db/archie.db` for the container's
  `/var/lib/archie/archie.db`;
- the repository checkout mounted at `/workspace`;
- the host Docker socket mounted into `archied`.

Parked worktrees are retained for investigation. Terminal cleanup behavior is
owned by the worktree/daemon lifecycle; do not delete paths manually while a
task is active.

## Understand NATS and container observation

The `ARCHIE_TASKS` JetStream stream uses file storage and work-queue retention
for `archie.task.>` and `archie.agent.>`.

| Plane | Subjects and behavior |
|---|---|
| Discovery | `archie.task.bug`, `.feature`, `.bootstrap`, or `.default`; daemon durable consumer `archie-daemon`, 5-minute ack wait, max 3 deliveries. |
| Legacy stages | `archie.agent.<id>.request`; worker durable consumer defaults to `archie-agent`, 30-minute ack wait, max 3 deliveries; response address travels in `X-Archie-Reply`. |
| Container task | Core NATS request/reply on `archie.taskrun.<id>`. A task container reads `/data/worktree/task.json` and creates a dedicated subscription; a shared worker uses queue group `archie-taskrun-workers`. |
| Privileged RPC | Store, forge, and worktree operations return to `archied` over core NATS subjects in `internal/storerpc`, `internal/forgerpc`, and `internal/worktreerpc`. |

Set `containers.network` explicitly. `config.docker.toml` uses
`archie-core_default`. Auto-detection inspects the daemon container; on failure,
spawned workers join Docker's default bridge and cannot resolve `nats` by
service name. An initial `ErrNoResponders` is retried for 20 seconds at 250 ms
intervals; other request errors are not readiness retries.

If `nats.token_env` is configured, `archied` refuses startup when that variable
is empty. It injects the resolved value as `NATS_TOKEN` into task containers.
It also forwards only configured provider-key variables. Compose forwards a
fixed environment list; adding a config reference without matching Compose
passthrough leaves the container empty.

## Fence current operational limitations

Treat these as open until code and tests prove otherwise:

- `subprocess` plus default `archie-agent` is protocol-incompatible.
- `config.production.toml` is not proof of a working deployment. Its identity
  uses legacy `token_env`, while current validation requires each identity's
  `forge.token` secret reference.
- Container-mode RPC servers use root forge/worktree objects for every
  identity; non-root identity tasks can use the wrong authority.
- Container orphan recovery and pool shutdown select every container labelled
  `archie-daemon=true`, without a daemon-instance label. One daemon can remove
  another daemon's workers on the same Docker host.
- `containers.max_uptime` only bounds the create/start context in
  `Pool.Acquire`; no running-container timer enforces the advertised lifetime.
- The container workflow builds `TaskContext` without an event bus. Do not
  expect in-process stage/agent event timelines to have container-mode parity.
- Live SSE clients drop events when their channel is full. Reconnect with the
  last event ID to replay at most 200 stored events.
- The dashboard binds `0.0.0.0:8484` in the Docker overlay and Compose publishes
  port 8484. It is unauthenticated and includes a mutating clear endpoint.

Route operational symptoms from these limitations to
`archie-debugging-playbook`; route fixes through change control.

## Understand deployment and rollback seams

The repository Gitea workflow runs on pushes to `main` and component tags. It
builds and pushes only:

- `git.catlow.cloud/sam/archied:latest`;
- `git.catlow.cloud/sam/archie-agent:latest`.

It injects component versions into labels/binary metadata but does not publish
versioned image tags. It runs no repository quality gate (`task check`, tests,
lint, or vet) before Docker builds; the Docker builds themselves compile the
binaries. Treat a successful workflow as image publication evidence only, not
application acceptance evidence.

Compose watchtower polls every 60 seconds with `--cleanup` and names
`archie-core-archied-1` plus `archie-core-archied-winter-1`. The workflow never
contacts the host directly. Do not infer that watchtower found, restarted, or
validated either live daemon.

Before any authorized promotion:

- record the running image IDs and immutable registry digests;
- record config file checksums without printing secrets;
- record task counts and active task IDs through safe GET observations;
- establish a NellDB-consistent backup procedure with `archie-nelldb`;
- define which prior digest will be restored and who may restart services.

The repository has no complete rollback command and publishes no versioned
image tags. Do not improvise a production rollback from `latest`; route it
through `archie-change-control`. Restoring a binary does not undo forge changes,
task transitions, or data-format changes.

## Treat carina facts as unverified external state

Repository guidance dated 2026-07-28 says two live instances run on `carina`
with distinct `bot_user` values. An archived PRD says this was confirmed on
2026-07-25, while `config.production.toml` proposes replacing them with one
multi-identity process and Compose watchtower still names two containers.

No live `carina` process list, config, image digest, service manager, or
deployment success was verified for this skill. Do not invent SSH, systemd, or
service commands. Query the approved operational source of truth under explicit
production-read authority before acting.

## Use the operator checklist

- [ ] Confirm the target host, config paths, bot identity, repos, and authority.
- [ ] Confirm secret variable names exist without printing values.
- [ ] Select one execution row from the mode matrix.
- [ ] Capture baseline logs, task summary, image digests, and config checksums.
- [ ] Start only the required services; exclude watchtower unless intended.
- [ ] Require NATS health when configured and `archied running` always.
- [ ] Observe NellDB state and forge-visible effects, not process status alone.
- [ ] Stop gracefully and allow deferred cleanup to finish.
- [ ] Confirm no tasks remain `running`; startup will requeue stale ones.
- [ ] Record open deviations and route behavior changes through validation and
      change control.

## Provenance and maintenance

Verified against repository state on **2026-07-28**. Re-run these one-line,
read-only checks after changes:

```bash
rg -n 'flag\.(String|Bool|Int64)' cmd/archied/main.go cmd/archie-agent/main.go
sed -n '1,120p' Taskfile.yml
sed -n '1,220p' docker-compose.yml
sed -n '1,220p' config.docker.toml
rg -n 'Mode|Command|NATS|Containers|WorkDir|DBPath|withDefaults|MaxUptime|PullPolicy' internal/config/config.go internal/config/indexer.go
rg -n 'SubprocessRunner|ServeOne|taskRunSubjectWildcard|archie-agent ready' internal/agentexec cmd/archie-agent
rg -n 'AutoRemove|archie-daemon|recoverOrphans|MaxUptime|NetworkMode' internal/container/pool.go
rg -n 'WorktreeMountDir|archie-cache-|archie-repo-|CleanupExpired' internal/storage/storage.go
rg -n 'HandleFunc|api/summary|api/tasks|events|ListenAndServe' internal/webui/webui.go
sed -n '1,220p' .gitea/workflows/deploy.yml
```
