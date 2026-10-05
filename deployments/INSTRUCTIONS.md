# Running this distribution

This archive contains five linux/amd64 binaries:

Each service owns its data and reaches the others only through their gRPC
contracts (or NATS, for task actions):

| Service | Owns | Reaches |
|---|---|---|
| `archie-state-store` | tasks, events, control-plane settings, identities and access policies, installed packages (PostgreSQL) | nothing |
| `archie-gateway` | conversations (PostgreSQL, its own tables) and every model call: chat turns, curators, MCP tools and sampling | State Store; daemon over NATS for task actions |
| `archied` | dispatch: forge polling, the task queue, the embedded NATS broker, agent containers | State Store; Gateway |
| `archie-messaging` | the Telegram, email and webhook channel connections | Gateway; State Store for channel settings |
| `archie-ui` | the dashboard and the webhook capture receiver | Gateway; State Store |

`archied playbooks lint` checks playbook binding YAML before it reaches the
daemon, and `archied playbooks serve` is its language server for editors.

Without `archie-messaging` the channels are simply absent, so it needs a unit
of its own (`deployments/systemd-user-service.md`).

`archie-agent` (the per-task sandboxed runtime) is **not** in this archive —
it only ever runs inside the Docker container `archied` spawns per task, never
as a host binary. Pull it from GHCR: `ghcr.io/samcharles93/archie-agent`.

## Quickstart

1. Pick a starting configuration. The full set of annotated templates lives
   in this repository's `deployments/` directory on GitHub
   (<https://github.com/samcharles93/archie-core/tree/main/deployments>) —
   `single-forge-github.toml`, `multi-forge-github-gitea.toml`,
   `local-ollama-standalone.toml`, and `docker-nats-stack.toml` cover the
   common cases. Copy whichever fits to:

   ```bash
   mkdir -p ~/.config/archie
   cp <template>.toml ~/.config/archie/config.toml
   ```

2. Edit `config.toml` (forge token, repos, `[services.state]` target, model
   providers) and place any secrets in `~/.config/archie/env` or referenced
   `secret://` locations.

3. Start the five processes. Order does not matter: a service whose peer is
   not up yet waits for it and reports it as degraded on its health surface
   meanwhile. The Gateway waits for the daemon's NATS endpoint when the broker
   is embedded.

   ```bash
   ./archie-state-store -config ~/.config/archie/config.toml -listen 127.0.0.1:9090 &
   ./archie-gateway     -config ~/.config/archie/config.toml -listen 127.0.0.1:8585 &
   ./archie-messaging   -config ~/.config/archie/config.toml &
   ./archie-ui          -config ~/.config/archie/config.toml &
   ./archied            -config ~/.config/archie/config.toml
   ```

   Dashboard reachability comes from `[web].listen` (default
   `127.0.0.1:8484`, which needs no dashboard token). For a non-loopback
   bind pass `-listen` with `-token` or `-token-file` to mint and persist
   one. `archie-ui` reads `GATEWAY_TOKEN` / `STATE_STORE_TOKEN` from its
   process environment only — it has no secret registry, so when those
   tokens live in a secret manager, whatever starts `archie-ui` must export
   them into its environment (the `[services.*].target_token` values in
   `config.toml` are the other supported source).

4. For a persistent, 24/7 deployment instead of a foreground run, follow the
   systemd user-unit runbook:
   <https://github.com/samcharles93/archie-core/blob/main/deployments/systemd-user-service.md>.

## Verifying an install

`archied playbooks lint -dir <playbooks-dir>` validates playbook binding YAML
against the same schema `archied` loads at startup — run it against your
playbooks directory before pointing `archied` at it.

## Version

This archive's binaries are built from the `archied` release tag in its
filename; `archie-ui`, `archie-gateway`, `archie-state-store` and
`archie-messaging` ship at that same version rather than
being independently tagged (see `RELEASING.md` in the source repository for why
only `archied` and `archie-agent` are independently versioned).

`archie-state-store`, `archie-gateway`, `archie-messaging`, `archie-ui` and
`archied` are services that must be running and addressable; `archie-messaging`
carries the Telegram, email and webhook channels since the v1.30.0 extraction, so
an install (or an upgrade across v1.30.0) needs its unit as well -- see
`deployments/systemd-user-service.md`.

### Native self-update

`archie-update-install` downloads the approved release's Linux/amd64 archive
and verifies its `SHA256SUMS` entry before executing or installing it. Releases
must include `release.json`, all host binaries, and the three updater scripts.
Older releases without this contract cannot be installed through the artifact
path. `--from-source` (or `ARCHIE_UPDATE_SOURCE_BUILD=1`) explicitly clones and
builds the selected release instead.

The update check command accepts `--channel stable` (default), `--channel next`
(includes prereleases), or `--channel exact-pin --pin 1.2.3`. Set these arguments
in the control plane's channel settings alongside the existing check command.
The notification destination remains separate from the release channel.

For scheduled, unattended runs, `archie-update-install --auto` discovers the
selected release and refuses if the daemon reports work in flight. It checks
again immediately before promotion. A scheduler can set
`ARCHIE_UPDATE_RELEASE_CHANNEL` and `ARCHIE_UPDATE_PIN`; nothing schedules updates
by default. All services stop through their ordinary graceful shutdown path.

`release.json` supplies the service order and required config sections to both
first-install and self-update. A missing unit or config section produces an
ordered migration plan and refuses installation. Apply that plan before retrying.
Automatic topology edits are intentionally unsupported.

The watchdog switches staged executables by rename, restarts services, and
requires the candidate daemon's existing readiness registry to remain healthy
for 120 seconds. Agent images come from the release manifest by digest. The
image switch updates the control-plane policy, preserving other settings;
rollback restores the prior image policy with conflict detection. Source builds
use the built image's immutable image ID.

Transactions live in `${XDG_STATE_HOME:-~/.local/state}/archie/update`, outside
the binary directory. `phase`, `environment`, `image.json` (when applicable),
and `previous/` describe the transaction. A lock reports the owning process.
On daemon boot, an interrupted transaction schedules independent recovery before
normal startup. To recover manually, run `transaction/watchdog --recover` from
that state directory. A failed recovery keeps the journal for another attempt.

Database snapshots remain available for manual recovery. Schema migrations and
PostgreSQL data are never automatically reversed.
