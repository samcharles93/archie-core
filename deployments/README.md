# Archie Core Deployment Examples

This directory contains configuration templates and guides for different deployment scenarios of Archie Core.

## Directory Structure

* **[`single-forge-github.toml`](./single-forge-github.toml)**:
  Standard single-forge deployment managing multiple GitHub repositories under a single bot account and GitHub token.

* **[`multi-forge-github-gitea.toml`](./multi-forge-github-gitea.toml)**:
  Multi-identity deployment running GitHub and a self-hosted Gitea instance simultaneously with distinct bot accounts, tokens, and repository sets.

* **[`local-ollama-standalone.toml`](./local-ollama-standalone.toml)**:
  Self-hosted deployment running local LLM models via Ollama (e.g., `llama3`, `qwen2.5`) with optional standalone (forge-disabled) operation.

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
archied
# or via systemd:
systemctl --user start archied
```
