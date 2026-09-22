# Managing `archied` with Systemd User Services

This runbook describes how to manage `archied` as a background user service on Linux servers.

---

## 1. Unit Files (written by `install.sh`)

`install.sh` writes all five units -- `archied.service`, `archie-state-store.service`,
`archie-gateway.service`, `archie-ui.service` and `archie-messaging.service` --
with the contents it
ships. This page describes what runs and why; the installer owns the contents,
so inspect a unit with `systemctl --user cat <unit>` rather than copying it from
here, where it would drift from what is written.

The Gateway unit is `archie-gateway.service`, written by `install.sh` beside
`archied.service`.

The State Store unit is `archie-state-store.service`. It owns the single `archie.db` SQLite
file and serves the `StateStore` gRPC contract. After the in-process store path
was deleted, BOTH `archied` and `archie-gateway` dial it via
`[services.state].target` (see the `archied` config below) and never open
`archie.db` themselves (`docs/prds/state-store-contract.md` §12 step 7). A
host-only agent uses the loopback bind with no token below; a container-mode
agent needs the Docker bridge gateway address plus a bearer token via
`--token`/`STATE_STORE_TOKEN` or `[services.state].target_token`, and a
non-loopback bind fails closed without one:

`install.sh` writes `archie-state-store.service` with that loopback bind.

---

### Messaging Service Unit (`~/.config/systemd/user/archie-messaging.service`)

The Telegram, email and webhook channels are served by `archie-messaging`, not
by `archied`, since the v1.30.0 extraction. `archied` starts no gateway for them
and logs nothing when one is absent, so a host that updates across v1.30.0
without this unit runs on with dead channels and a daemon that looks perfectly
healthy -- that was `archie-core-1c01`, found after two days of a silently dead
Telegram bot. `install.sh` writes this unit on a fresh install;
`scripts/archie-update-install` refuses an
update when a host is missing a unit and names what is missing, which now fires
for a host that drifted or predates this release rather than the normal path.

It dials the Gateway for the chat contract and the State Store for the stored
channel settings, and reads the same `config.toml`. Like `archie-ui` it resolves
`GATEWAY_TOKEN` and `STATE_STORE_TOKEN` from its process environment, hence the
`EnvironmentFile`:

`install.sh` writes `archie-messaging.service`, whose `After=` and `Wants=`
lines preserve that store-then-gateway order.

## 2. UI Service Unit (`~/.config/systemd/user/archie-ui.service`)

The dashboard is served by the standalone UI Service, not by `archied`
(UI cutover, `archie-core-8cda.5.4`). It reads the same `config.toml`, dials
the Gateway and State Store targets already in it, and binds `[web].listen`.
A non-loopback bind requires a dashboard token (`-token`, or `-token-file` to
mint and persist one);

**Auth note (deliberate design, `archie-core-8cda.5.5`):** unlike `archied`,
`archie-ui` has no secret registry — it resolves `GATEWAY_TOKEN` and
`STATE_STORE_TOKEN` from its **process environment only** (or from the
`target_token` values in `config.toml`). Whatever starts `archie-ui` is
therefore responsible for exporting those tokens into its environment: keep
them in `~/.config/archie/env` (loaded by the `EnvironmentFile` line below),
not only in a secret manager. A deployment that authenticates `archied`
through bws/Vault but exports nothing for `archie-ui` leaves the dashboard
unable to dial its dependencies.

For supervision, `GET /healthz` on `[web].listen` is the process's liveness
probe (token-free by design); the authenticated readiness surface lives at
the dashboard's health endpoint behind the token. A watchdog can curl
`/healthz` without credentials.

`install.sh` writes `archie-ui.service`. It has no secret registry of its own,
so its token handling is the `EnvironmentFile` note above.

Concretely, for a deployment whose dependency tokens live outside
`config.toml`, `~/.config/archie/env` carries the two names and the unit
loads it; the dashboard token itself comes from `-token-file` (there is no
environment variable for it):

```bash
# ~/.config/archie/env
GATEWAY_TOKEN=...      # presented to archie-gateway
STATE_STORE_TOKEN=...  # presented to archie-state-store
```

`After=` orders the start, it does not wait for the store to bind. The UI
process closes that gap itself: its event pump retries priming with backoff
until the State Store answers, so the activity feed goes live once the store
is up rather than staying history-only until the next restart.

For a non-loopback bind, override the installed unit's `ExecStart` with
`-token-file %h/.local/share/archie/web-token` (or `-token`) in a drop-in rather
than editing the installer's copy in place.

---

## 3. Enabling User Linger (`loginctl`)

By default, systemd terminates user services when you log out of SSH. Enabling **linger** allows your `archied` service to run continuously 24/7 across reboots and logouts:

```bash
loginctl enable-linger "$USER"
```

Verify linger status:
```bash
loginctl show-user "$USER" | grep Linger
# Output: Linger=yes
```

---

## 4. Service Commands

Reload systemd daemon files:
```bash
systemctl --user daemon-reload
```

Enable and start the service immediately:
```bash
systemctl --user enable --now archie-state-store archie-gateway archie-messaging archie-ui archied
```

Check service status:
```bash
systemctl --user status archied
```

Tail live daemon logs:
```bash
journalctl --user -u archied -f
```

Restart or stop the service:
```bash
systemctl --user restart archied
systemctl --user stop archied
```

---

## 5. Default: Native Daemon, Embedded NATS, Managed Containers

Embedded NATS is the default. The systemd service runs `archied` natively, so
interactive chat keeps the host access the operator configured, while every
autonomous repository workflow runs in a task-scoped Docker container. No
Compose service or host `archie-agent` binary is required.

Docker must be installed and reachable by the service user. With no explicit
broker or container settings, Archie defaults `containers.image` to
`ghcr.io/samcharles93/archie-agent:latest`. Every profile shipped in
`deployments/` overrides that default with a pinned reference -- see
[Pinning the archie-agent image](#pinning-the-archie-agent-image) below. An
explicit equivalent, pinned the same way, is:

```toml
[nats]
mode = "embedded"

[containers]
image = "ghcr.io/samcharles93/archie-agent@sha256:870f1b1357979cfea23fccacb24166ebeb9ab321708ef2c2b218a763ed99b090"
pull_policy = "missing"
```

At startup Archie resolves the Docker bridge used by its task containers,
binds the authenticated embedded broker only to that bridge's host gateway,
and passes the resulting endpoint to each worker. There is no port to publish
and no broker URL to maintain.

The State Store is a separate process and, with a container-mode agent, must
be reachable from the container, so bind it to the same bridge gateway with a
token and point `[services.state].target` at it (start
`archie-state-store` with `-listen <bridge>:9090 -token <token>`); without a
token a non-loopback bind fails closed. For a host-only agent, `127.0.0.1:9090`
with no token is correct.

If Docker or the image is unavailable, `archied` still serves chat and the
dashboard. Autonomous tasks park with an explicit capability error; they never
fall back to a host model loop.

---

## 6. Pinning the archie-agent Image

`ghcr.io/samcharles93/archie-agent:latest` is a moving target: every push to
`main` that touches the runtime's package closure re-publishes it (see
`.github/workflows/deploy.yml`), so `latest` can change under a running
deployment with no warning and no way to reproduce a prior build.

As of this writing, `archie-agent` has **no published semantic-version tag**
-- CI only pushes `:latest` to GHCR (`archied` is versioned independently per
`RELEASING.md`, but that versioning is not yet reflected as a registry tag for
the agent image). Until CI publishes one, the only reproducible reference is
a content digest, resolved once and pinned explicitly:

```toml
[containers]
image = "ghcr.io/samcharles93/archie-agent@sha256:870f1b1357979cfea23fccacb24166ebeb9ab321708ef2c2b218a763ed99b090"
```

This is the digest `latest` resolved to on 2026-09-05. Digest pinning is the
strongest option for production: it is immutable and independently
verifiable (`docker manifest inspect ghcr.io/samcharles93/archie-agent@sha256:...`),
at the cost of being opaque in a config file a human is meant to edit --
resolve a fresh digest deliberately (`docker pull ...:latest` followed by
`docker inspect --format '{{index .RepoDigests 0}}'`) rather than editing this
one in place expecting it to track new releases.

If/when CI starts publishing a semantic-version tag for `archie-agent`
(e.g. `ghcr.io/samcharles93/archie-agent:v1.21.0`), prefer that tag for
day-to-day config readability, and reserve digest pinning for deployments
that need byte-for-byte reproducibility guarantees a tag alone cannot give
(tags are mutable pointers; a compromised or accidentally-overwritten tag
still resolves, a digest cannot).

The bundled update adapter snapshots the configured `[containers].image` before
rebuilding it and restores that image if the restarted daemon fails its health
check. A custom image reference is therefore supported directly in
`config.toml`; `ARCHIE_AGENT_IMAGE` is only an override for deployments that
cannot make that configuration file available to the update command.

## 7. Optional: External Compose NATS

External NATS changes only broker deployment. It does not change the worker
executor. Start the optional service and configure the pinned Compose gateway:

```bash
docker compose up -d nats
```

```toml
[nats]
mode = "external"
url = "nats://172.19.0.1:4222"

[containers]
network = "archie-core_default"
```

The daemon hands workers the URL its own client connected to. The Compose
service name is not resolvable from the native host, and localhost inside a
worker is the worker itself; the pinned network gateway is reachable from both.

`pull_policy = "always"` cannot authenticate to a private registry because the
daemon sends no registry credentials to the Docker API. Refresh a private image
with the operator's Docker credentials and retain `pull_policy = "missing"`:

```bash
docker compose pull agent
```

`db_path` and other paths are native host paths. Point them at the intended
state directory; changing from an old containerized daemon path can otherwise
start Archie against empty state files.
