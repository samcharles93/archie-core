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

The State Store service (`archie-state-store`) is `.4.3`: it owns the single
`archie.db` task SQLite file and serves the `StateStore` gRPC contract. It is
not yet consumed by the daemon (which still serves the store in-process), so
running it now is only needed once the daemon points at it — the loopback
bind above is the safe default, and a non-loopback bind requires a bearer
token (`--token` / `STATE_STORE_TOKEN` / `[services.state].target_token`).
