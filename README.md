[![Quality](https://github.com/samcharles93/archie-core/actions/workflows/quality.yml/badge.svg)](https://github.com/samcharles93/archie-core/actions/workflows/quality.yml)

# Archie

Archie is an event-driven agentic automation platform. It accepts work from forge issues and other events, routes it through configurable workflows, runs agents in isolated worktrees, and presents the results for human review. It also provides a dashboard and conversational channels for interacting with agents.

## What it does

- **Automates repository work:** Watches GitHub or Gitea for configured issues, runs a workflow, applies repository gates, and opens a pull request for review.
- **Responds to events:** Captures webhooks, maps payload fields, and dispatches approved playbook bindings into workflows. See the [first playbook guide](docs/guides/first-playbook.md).
- **Keeps execution isolated:** Runs agent work in separate git worktrees and supports containerized agents. The daemon coordinates work while the State Store owns durable task data.
- **Supports operator interaction:** Provides a dashboard, a Gateway for conversations, and messaging channels. See the [architecture decision index](docs/prds/01-project-management.md) for the boundaries and ongoing migration.

## Develop locally

The repository requires Go 1.27, [Task](https://taskfile.dev/), Node.js and npm, and Docker with a running daemon. The development stack starts PostgreSQL 18 through Docker Compose. Install the Go formatting and lint tools listed in [CLAUDE.md](CLAUDE.md#build--test) before running the full quality gate.

```bash
task dev
```

Open <http://localhost:5173> for the dashboard with Vite hot reload. `task dev` starts the daemon, Gateway, State Store, UI service, and frontend development server; it uses [`deployments/dev.toml`](deployments/dev.toml) as a local configuration overlay. Set credentials and other required settings in your local Archie configuration as needed.

Useful checks:

```bash
task build       # build the service and agent binaries in bin/
task test        # run the short Go test suite
task check       # run the repository quality gate
```

## Deploy

Start with the [deployment examples](deployments/README.md). They cover a single GitHub forge, multiple GitHub and Gitea identities, local Ollama, and an external NATS stack. Copy a suitable template to `${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`, then set its repository, model, credential, and service settings for your environment. The examples describe the required PostgreSQL connection and how to start each process. A [systemd user service guide](deployments/systemd-user-service.md) is available, but systemd is optional.

## Documentation

- [Architecture decisions](docs/prds/01-project-management.md) — approved design and open migration questions.
- [Development guides](docs/development/index.md) — change checklists by area.
- [Deployment examples](deployments/README.md) — configuration templates and operating notes.
- [Release process](RELEASING.md) — how releases are built and published.

Contributions should follow [CLAUDE.md](CLAUDE.md), including its testing, generated-file, and commit rules.
