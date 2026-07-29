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
