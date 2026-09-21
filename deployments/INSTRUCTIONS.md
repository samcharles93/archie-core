# Running this distribution

This archive contains six linux/amd64 binaries:

- **`archied`** — the resident daemon: polls your forge for assigned issues
  and dispatches them into sandboxed `archie-agent` containers. It serves no
  dashboard: the web UI is `archie-ui`'s process (UI cutover,
  `archie-core-8cda.5.4`).
- **`archie-ui`** — serves the operator dashboard and the webhook capture
  receiver, dialing the Gateway and State Store gRPC contracts. Reads only
  `[services.*]`, `[web]` and `[capture]` from the shared `config.toml`.
- **`archie-gateway`** — serves the chat (`ChatContract`) surface `archied`,
  `archie-ui` and `archie-messaging` dial. Runs as its own process; `archied`
  never embeds a gateway.
- **`archie-messaging`** — serves the Telegram, email and webhook channels,
  dialing the Gateway for the chat contract and the State Store for the stored
  channel settings. `archied` runs no gateway for those channels, so without
  this process they are simply absent — and nothing logs an error
  (`archie-core-1c01`), which is why it has a unit of its own in
  `deployments/systemd-user-service.md`.
- **`archie-state-store`** — owns the single `archie.db` SQLite file behind
  a gRPC contract. `archied` and `archie-gateway` are gRPC clients of it, not
  owners of the database.
- **`archie-playbooks`** — standalone CLI to lint playbook binding YAML
  before it reaches `archied`.

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

3. Start the five processes, in order (`archie-state-store` and
   `archie-gateway` first — `archied`, `archie-ui` and `archie-messaging` dial
   both at startup):

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

`archie-playbooks lint -dir <playbooks-dir>` validates playbook binding YAML
against the same schema `archied` loads at startup — run it against your
playbooks directory before pointing `archied` at it.

## Version

This archive's binaries are built from the `archied` release tag in its
filename; `archie-ui`, `archie-gateway`, `archie-state-store`,
`archie-messaging` and `archie-playbooks` ship at that same version rather than
being independently tagged (see `RELEASING.md` in the source repository for why
only `archied` and `archie-agent` are independently versioned).

`archie-state-store`, `archie-gateway`, `archie-messaging`, `archie-ui` and
`archied` are services that must be running and addressable; `archie-messaging`
carries the Telegram, email and webhook channels since the v1.30.0 extraction, so
an install (or an upgrade across v1.30.0) needs its unit as well -- see
`deployments/systemd-user-service.md`. `archie-playbooks` is a CLI and runs on
demand.
