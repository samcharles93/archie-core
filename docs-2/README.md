# Tau CLI Reference

Tau is a provider-agnostic coding agent with an interactive terminal UI. It provides an extensible, personalised
environment for working with AI models, featuring an interactive TUI, an optional Web UI, session history, token
tracking, and plugin integration.

## Usage

```bash
tau [global flags] [command]
```

Running `tau` with no arguments starts an interactive chat session and also starts a local Web UI on `127.0.0.1`. You do
not need a `--model` flag; if no model is configured the session starts unselected and you pick one with `/model`.

## Global Flags

| Flag                          | Description                                                               |
| ----------------------------- | ------------------------------------------------------------------------- |
| `--provider <name>`           | Provider name to use (e.g. `deepseek`, `openrouter`). Env: `TAU_PROVIDER` |
| `--model <id>`                | Model ID to use. Also accepts `provider:model-id` or `provider/model-id`  |
| `--max-tokens <n>`            | Maximum completion tokens per response                                    |
| `--temperature <f>`           | Sampling temperature (0–2)                                                |
| `--resume`, `-r <id\|latest>` | Resume a saved session by ID, or `latest`                                 |
| `--execute`, `-x <text>`      | Execute mode: process prompt, print response, exit (reads stdin when piped) |
| `--prompt`, `-p <text>`       | [deprecated] Use `-x`/`--execute` instead. One-shot / execute mode         |
| `--jsonl`, `--stream-json`    | Output framed JSONL events on stdout instead of plain text (execute mode)   |
| `--child`                     | Run as a headless agent child process (internal use; hidden)              |
| `--web`                       | Start the Web UI and open it in the default browser                       |
| `--port <n>`                  | HTTP port for the Web UI. Default `0` = OS-assigned ephemeral port        |
| `--no-web`                    | Do not start the Web UI. TUI only                                         |
| `--insecure`                  | Skip TLS certificate verification. Env: `TAU_INSECURE`                    |
| `--verbose`                   | Show debug messages on stderr. Env: `TAU_VERBOSE`                         |

## Enabling Providers

Tau discovers providers automatically. To enable a provider:

1. **`tau setup` (recommended for first run):** Interactive walkthrough for selecting a provider and authenticating.
   Managed API keys are stored in `~/.config/tau/auth.yaml` — env vars override them when both are present.

2. **`tau provider` CLI:**
   ```bash
   tau provider list                      # show all providers and auth state
   tau provider login deepseek            # enter and store a managed API key
   tau provider login openai-codex        # OAuth device-code flow
   tau provider login                     # interactive selector (TTY only)
   tau provider logout openai             # disable and clear credentials
   ```
   For non-OAuth providers, `tau provider login` stores a managed API key in `auth.yaml`.

3. **Environment variable:** Export the provider's API key env var (e.g. `DEEPSEEK_API_KEY=sk-...`). Tau
   detects it at startup.

4. **TUI slash commands:** `/provider [name]` to toggle a provider, `/provider login <name>` for OAuth flow.

5. **config.yaml:** Add a provider block (see Configuration below).

Run `/model` in the TUI to pick from all enabled providers' models.

## Web UI

When tau starts interactively, it binds a local HTTP/WebSocket server on `127.0.0.1`. The URL is shown in the TUI status
bar.

```bash
./tau              # TUI + Web UI, URL in status bar
./tau --web        # TUI + Web UI, also opens the browser
./tau --no-web     # TUI only
./tau --port 9343  # Fixed port
```

The browser connects to `/ws` and receives the same event stream as the TUI. Commands sent from the browser are
forwarded to the coordinator identically to TUI commands. Multiple browser tabs may connect to the same session.

## Slash Commands (TUI)

| Command                                       | Description                                                                                                           |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `/model [id]`                                 | Switch model; opens picker when called without an ID                                                                  |
| `/provider [name]`                            | Toggle a provider on/off; lists all providers when called without an argument                                         |
| `/provider login <provider>`                  | OAuth device-code sign-in for `github-copilot` or `openai-codex`; opens the browser and copies the code when possible |
| `/provider logout <provider>`                 | Disable a provider / remove its saved key                                                                             |
| `/refresh`                                    | Re-discover models from all enabled providers                                                                         |
| `/effort [level]`                             | Set reasoning effort (`off`, `low`, `medium`, `high`, `max`)                                                          |
| `/reasoning [on\|off]`                        | Toggle display of model reasoning content                                                                             |
| `/system <prompt>`                            | Set the session system prompt                                                                                         |
| `/session [list\|info\|export\|delete\|<id>]` | Manage saved sessions                                                                                                 |
| `/resume [id]`                                | Resume a saved session                                                                                                |
| `/new`                                        | Start a fresh conversation (aliases: `/clear`, `/reset`)                                                              |
| `/reload`                                     | Reload extensions/plugins                                                                                             |
| `/help`                                       | Show all available commands                                                                                           |
| `/exit`                                       | Quit tau                                                                                                              |

## Subcommands

### `tau setup`

Interactive setup wizard for first-run configuration. Walks through provider selection, authentication (API key or OAuth), and model selection.

```bash
tau setup
```

Called automatically on first run if no providers are configured.

### `tau provider`

Manage provider authentication from the CLI.

```bash
tau provider list                       # table of providers with status, source, and auth type
tau provider login [name]               # authenticate an OAuth or API-key provider
tau provider login                      # interactive selector when stdin is a TTY
tau provider logout <name>              # disable provider and clear managed credentials
```

Provider credentials are managed via the keychain or stored in `~/.config/tau/auth.yaml`. See [`docs/providers.md`](providers.md) for the full provider architecture.

### `tau models`

List available models from the configured provider.

```bash
tau models [--json]
```

Uses the live models.dev catalog (network). For the model list used in the interactive TUI, see the embedded snapshot
(`internal/providers/snapshot/`).

### `tau refresh`

Force a refresh of the models.dev network catalog and list models.

```bash
tau refresh
```

Downloads the latest catalog for the configured provider. This affects the `tau models` output and the snapshot
generator, but interactive sessions use the embedded offline snapshot (`internal/providers/snapshot/models.json`).

### `tau sessions`

List saved chat sessions.

```bash
tau sessions
```

Shows a table of sessions with ID, model, message count, tokens, cost, and date. Use `/session` inside the TUI for
richer session management.

### `tau token`

Print the resolved API key / bearer token for the selected provider.

```bash
tau token
```

Useful for debugging auth configuration.

### `tau update`

Update the current tau binary from GitHub release assets, or check for available updates.

```bash
tau update [--check] [--version v0.16.2] [--repo owner/repo] [--force]
```

The updater downloads the platform archive and `checksums.txt`, verifies the archive checksum, extracts the tau binary,
and applies it in place.

By default, update checks only happen when you run `tau update` manually. Set
`updates.mode: warn` in [configuration](configuration.md#updates) to keep manual
checks with update notifications, or `updates.mode: disabled` to disable all update
checks including `tau update`. Dev builds (version `dev`) are always excluded
from update checks — only release builds are eligible.

## Configuration

Tau loads `~/.config/tau/config.yaml` (global) and `.tau.yaml` (project-local). Example configs are in
[`config-example.yaml`](config-example.yaml) and [`config-deepseek-example.yaml`](config-deepseek-example.yaml).

### Minimal configuration

```yaml
# ~/.config/tau/config.yaml
default_provider: deepseek
default_model: deepseek-chat

providers:
  - name: deepseek
    base_url: https://api.deepseek.com/v1
    auth:
      type: api_key
      api_key_env: DEEPSEEK_API_KEY
```

Model metadata (context window, pricing, reasoning) is loaded from the embedded snapshot - you do not need to repeat it
in config. See [`docs/ai-sdk.md`](ai-sdk.md) for full integration details.

### `ProviderConfig` fields

| Field              | Description                                                                              |
| ------------------ | ---------------------------------------------------------------------------------------- |
| `name`             | Provider identifier (matches tau's catalog ID)                                           |
| `base_url`         | API endpoint. Must include `/v1` when the provider uses it                               |
| `type`             | ai-sdk class override (`anthropic` for Anthropic native API; omit for OpenAI-compatible) |
| `auth.type`        | `api_key`, `none`, or `oauth_pkce`                                                       |
| `auth.api_key_env` | Environment variable name for the API key                                                |
| `auth.api_key`     | Literal API key (not recommended; prefer `api_key_env`)                                  |
| `headers`          | Optional static request headers; do not put bearer tokens here                           |
| `models`           | Optional list of model overrides (see below)                                             |

### Model overrides

Tau's embedded snapshot provides model metadata. Override individual fields only when you need to customise them:

```yaml
providers:
  - name: openai
    auth:
      type: api_key
      api_key_env: OPENAI_API_KEY
    models:
      - id: gpt-5.4
        context_window: 200000
        default_max_tokens: 16384
```

## Documentation Index

### Architecture & Design

- [Architecture Overview](architecture.md) - system design, communication patterns, layer map
- [Event Bus Architecture](eventbus.md) - type-based event routing, publishers, subscribers

### Core Systems

- [Agent Coordinator](agent.md) - turn loop, command handlers, tool execution, prompts
- [Chat Types](chat-types.md) - complete command/event reference, core types
- [Tools](tools.md) - registry, built-in tools, mutation queue, UIBridge
- [Commands](commands.md) - slash command registry, custom commands

### UI Systems

- [Terminal UI (TUI)](tui.md) - inline rendering, widget tree, slash commands, completions, notifications
- [Web UI](webui.md) - Vue 3 SPA, state management, components, protocol, build
- [Server & Bridge](server.md) - HTTP/WebSocket server, event fan-out, wire format
- [taui Framework](taui.md) - standalone TUI rendering engine, widgets, color system

### Infrastructure

- [Configuration](configuration.md) - YAML config format, env vars, CLI flags
- [Providers](providers.md) - provider catalogue, auth state, model discovery, dynamic switching
- [AI SDK Integration](ai-sdk.md) - embedded snapshot, dynamic streamer, URL rules, reasoning
- [Sessions](sessions.md) - SQLite persistence, session lifecycle, JSONL export
- [Skills](skills.md) - SKILL.md discovery, frontmatter parsing, prompt rendering

### Extensibility

- [Plugin SDK](plugins.md) - gRPC extension system, tools, slash commands, lifecycle events
