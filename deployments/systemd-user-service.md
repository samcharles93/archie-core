# Managing `archied` with Systemd User Services

This runbook describes how to manage `archied` as a background user service on Linux servers.

---

## 1. Unit File (`~/.config/systemd/user/archied.service`)

Create the systemd user service file at `~/.config/systemd/user/archied.service`:

```ini
[Unit]
Description=Archie Core Orchestrator Daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/archied -config %h/.config/archie/config.toml
EnvironmentFile=-%h/.config/archie/env
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
```

---

## 2. Enabling User Linger (`loginctl`)

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

## 3. Service Commands

Reload systemd daemon files:
```bash
systemctl --user daemon-reload
```

Enable and start the service immediately:
```bash
systemctl --user enable --now archied
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

## 4. Default: Native Daemon, Embedded NATS, Managed Containers

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

If Docker or the image is unavailable, `archied` still serves chat and the
dashboard. Autonomous tasks park with an explicit capability error; they never
fall back to a host model loop.

---

## 5. Pinning the archie-agent Image

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

## 5. Optional: External Compose NATS

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
