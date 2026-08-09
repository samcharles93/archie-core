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

## 4. Running Alongside the Docker Stack

`archied` on the host still needs NATS and still spawns agent containers, so
`docker compose up -d` keeps running the bus and holds the network the agents
join. Two things bite in this hybrid layout:

**One NATS URL serves two network namespaces.** The daemon hands agent
containers its own `nats.url` verbatim, so `nats://nats:4222` (resolvable only
inside compose) and `nats://127.0.0.1:4222` (the container's own loopback) are
both wrong. Use the compose network's gateway address, which the host and the
containers can both reach:

```toml
[nats]
url = "nats://172.19.0.1:4222"
```

`docker-compose.yml` pins the subnet so that address cannot drift when the
network is recreated.

**`pull_policy` must be `"missing"` against a private registry.** The daemon
calls the Docker API with no registry credentials, so `"always"` gets a 401,
which fails the container pool and exits the daemon. Refresh the agent image
by hand instead:

```bash
docker compose pull agent
```

**Paths.** Under compose the daemon saw bind-mounted paths; on the host it sees
the real ones. In particular `db_path` is the state-path prefix from which the
daemon derives independent task and conversation SQLite files; it must point
at the intended host location, or the daemon starts against empty files.
