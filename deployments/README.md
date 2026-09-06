# Archie Core Deployment Examples

This directory contains configuration templates and guides for different deployment scenarios of Archie Core.

## Directory Structure

* **[`single-forge-github.toml`](./single-forge-github.toml)**:
  Standard single-forge deployment managing multiple GitHub repositories under a single bot account and GitHub token.

* **[`multi-forge-github-gitea.toml`](./multi-forge-github-gitea.toml)**:
  Multi-identity deployment running GitHub and a self-hosted Gitea instance simultaneously with distinct bot accounts, tokens, and repository sets.

* **[`local-ollama-standalone.toml`](./local-ollama-standalone.toml)**:
  Self-hosted deployment running local LLM models via Ollama (e.g., `llama3`, `qwen2.5`) with optional standalone (forge-disabled) operation.

* **[`docker-nats-stack.toml`](./docker-nats-stack.toml)**:
  Host-run `archied` orchestrating sandboxed `archie-agent` containers over the repository's Compose-managed NATS service. Copy to `~/.config/archie/config.toml`, start NATS with Compose, then start `archied` on the host.

* **[`systemd-user-service.md`](./systemd-user-service.md)**:
  Operational runbook for running `archied` as a persistent 24/7 background service via systemd user units and `loginctl enable-linger`.

---

## Usage

Copy any scenario template to your XDG configuration directory (`${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`):

```bash
mkdir -p ~/.config/archie
cp deployments/single-forge-github.toml ~/.config/archie/config.toml
```

Edit API keys in `~/.config/archie/env` or directly in `config.toml`, then launch Archie Core:

```bash
archie-gateway -config ~/.config/archie/config.toml -listen 127.0.0.1:8585 &
archie-state-store -config ~/.config/archie/config.toml -listen 127.0.0.1:9090 &
archied -config ~/.config/archie/config.toml
# or via systemd:
systemctl --user start archied
```

The templates use the extracted Gateway Service by default. Start
`archie-gateway` before `archied`; it owns the conversation SQLite file and
serves the `ChatContract` gRPC API on the configured target.

The State Store service (`archie-state-store`) owns the single `archie.db` task
SQLite file and serves the `StateStore` gRPC contract. After the in-process
store path was deleted, BOTH `archied` and `archie-gateway` dial it via
`[services.state].target` and never open `archie.db` themselves
(`docs/prds/state-store-contract.md` §12 step 7). The templates set
`[services.state]` accordingly: a host-only agent reaches a loopback bind
(`127.0.0.1:9090`) with no token, while a container-mode agent needs the
Docker bridge gateway address plus a matching bearer token (`target_token` /
`--token` / `STATE_STORE_TOKEN`), because a container cannot reach the host's
loopback.

```bash
# loopback (host-only agent)
archie-state-store -config ~/.config/archie/config.toml -listen 127.0.0.1:9090
# or container-mode: bind the bridge gateway with a token
archie-state-store -config ~/.config/archie/config.toml -listen 172.17.0.1:9090 -token <token>
```

## Verifying the State Store

`archie-state-store` exposes an optional HTTP readiness surface when launched
with `-ready-addr` (the systemd unit uses `127.0.0.1:9091`). Use it to confirm
the process is up and the store is healthy before starting `archied`:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9091/healthz        # 200 = process up
curl -s http://127.0.0.1:9091/health/detailed                                # state_db component -> 200, or 503 when degraded
```

A running State Store owns exactly one task SQLite file (`<db_path>-tasks.sqlite`);
`archied` and `archie-gateway` never open `archie.db` directly, so a second
store process on the same `db_path`, or a consumer that opens the task file,
indicates dual-store ownership (`docs/prds/state-store-contract.md` §12 step 7/8).
The readiness probe is the single-writer check the runbook has against a
degraded store: a `503` from `/health/detailed` means `archied` should be
started only after the store is healthy.
